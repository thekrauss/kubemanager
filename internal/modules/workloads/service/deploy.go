package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/activities"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	projectRepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
)

const (
	defaultChartName    = "standard-app"
	defaultChartVersion = "0.1.0"
)

func defaultIfEmpty(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// RunAsAuthorizer : contrôle « exécuter comme un compte de service »
// (implémenté par *iam.ServiceAccountService).
type RunAsAuthorizer interface {
	AuthorizeRunAs(ctx context.Context, caller iam.Principal, projectID uuid.UUID, ref string) (uuid.UUID, error)
}

// IdentitySettings : identité des workloads (iam.md §7.2). nil : désactivée.
type IdentitySettings struct {
	Authorizer RunAsAuthorizer
	Audience   string // émetteur du STS
	TokenURI   string
}

// ErrWorkloadIdentityDisabled : pas de cluster ou de STS configuré.
var ErrWorkloadIdentityDisabled = errors.New("workload identity is not enabled on this platform")

type WorkloadService struct {
	// Identity : nil si l'identité des workloads est désactivée.
	Identity    *IdentitySettings
	Distributor temporal.TaskDistributor
	Repo        repository.WorkloadRepository
	ProjectRepo projectRepo.ProjectRepository
	K8sClient   *kubernetes.Clientset
	K8sConfig   *rest.Config
	Logger      *zap.SugaredLogger
}

// Interface compliance check — erreur de compil si méthodes manquantes.
var _ IWorkloadService = (*WorkloadService)(nil)

func NewWorkloadService(
	dist temporal.TaskDistributor,
	repo repository.WorkloadRepository,
	pRepo projectRepo.ProjectRepository,
	k8s *kubernetes.Clientset,
	k8sCfg *rest.Config,
	log *zap.SugaredLogger,
) *WorkloadService {
	return &WorkloadService{
		Distributor: dist,
		Repo:        repo,
		ProjectRepo: pRepo,
		K8sClient:   k8s,
		K8sConfig:   k8sCfg,
		Logger:      log.With("service", "WorkloadService"),
	}
}

type WorkloadServiceRequest struct {
	domain.CreateWorkloadRequest
	// Caller : auteur de la demande (contrôle actAs du compte de service).
	Caller iam.Principal
}

func (s *WorkloadService) identity(saID uuid.UUID) *activities.WorkloadIdentity {
	return &activities.WorkloadIdentity{ServiceAccount: string(iam.ServiceAccountPrincipal(saID)),
		Audience: s.Identity.Audience, TokenURI: s.Identity.TokenURI}
}

func (s *WorkloadService) Deploy(ctx context.Context, in *WorkloadServiceRequest) (*domain.Workload, error) {
	pID, _ := uuid.Parse(in.ProjectID)

	currentCPU, currentMem, currentStorage, _ := s.Repo.GetTotalUsageByProject(ctx, pID)

	project, err := s.ProjectRepo.GetProjectByID(ctx, pID.String())
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeNotFound, "project not found")
	}

	replicas := int64(in.Replicas)
	if replicas <= 0 {
		replicas = 1
	}

	if err := s.validateNetwork(in.TargetPort); err != nil {
		return nil, err
	}

	requestedTotalCPU := s.parseCPU(in.CPULimit) * replicas
	limitCPU := s.parseCPU(project.CpuLimit)
	if (currentCPU + requestedTotalCPU) > limitCPU {
		return nil, betoerrors.Newf(betoerrors.CodeInvalidInput, "quota exceeded: CPU limit reached (%d/%d m)", currentCPU+requestedTotalCPU, limitCPU)
	}

	requestedTotalMem := s.parseMemory(in.MemoryLimit) * replicas
	limitMem := s.parseMemory(project.MemoryLimit)
	if (currentMem + requestedTotalMem) > limitMem {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "quota exceeded: Memory limit reached")
	}

	if in.PersistenceEnabled {
		requestedStorage := s.parseStorage(in.StorageSize)
		limitStorage := s.parseStorage(project.StorageLimit)
		if (currentStorage + requestedStorage) > limitStorage {
			return nil, betoerrors.New(betoerrors.CodeInvalidInput, "quota exceeded: Storage limit reached")
		}
	}

	_ = currentStorage

	targetNamespace := fmt.Sprintf("km-%s", project.Name)

	// Identité : vérifiée avant toute création (actAs sur le compte).
	var runAs *uuid.UUID
	if in.ServiceAccount != "" {
		if s.Identity == nil {
			return nil, ErrWorkloadIdentityDisabled
		}
		saID, err := s.Identity.Authorizer.AuthorizeRunAs(ctx, in.Caller, pID, in.ServiceAccount)
		if err != nil {
			return nil, err
		}
		runAs = &saID
	}

	workload := &domain.Workload{
		ID:                 uuid.New(),
		ProjectID:          pID,
		Name:               in.Name,
		Namespace:          targetNamespace,
		ChartName:          defaultChartName,
		Version:            defaultChartVersion,
		Image:              in.Image,
		Replicas:           in.Replicas,
		CPULimit:           in.CPULimit,
		MemoryLimit:        in.MemoryLimit,
		TargetPort:         in.TargetPort,
		PersistenceEnabled: in.PersistenceEnabled,
		StorageSize:        in.StorageSize,
		StorageClass:       defaultIfEmpty(in.StorageClass, "local-path"),
		Status:             "STARTING",
		CurrentPhase:       "HELM_CHART_LOADING",
		ServiceAccountID:   runAs,
	}

	if err := s.Repo.Create(ctx, workload); err != nil {
		return nil, err
	}

	wfInput := workflows.DeployWorkloadInput{
		WorkloadID:         workload.ID.String(),
		ProjectID:          in.ProjectID,
		Namespace:          targetNamespace,
		ReleaseName:        in.Name,
		Image:              in.Image,
		EnvVars:            in.EnvVars,
		Secrets:            in.SecretData,
		Replicas:           in.Replicas,
		PersistenceEnabled: in.PersistenceEnabled,
		StorageSize:        in.StorageSize,
		StorageClass:       in.StorageClass,
		MountPath:          in.MountPath,
		TargetPort:         in.TargetPort,
		ServiceType:        in.ServiceType,
		CPURequest:         in.CPURequest,
		MemoryRequest:      in.MemoryRequest,
		CPULimit:           in.CPULimit,
		MemoryLimit:        in.MemoryLimit,
		RunAsUser:          in.RunAsUser,
		RunAsGroup:         in.RunAsGroup,
		ReadOnlyRootFS:     in.ReadOnlyRootFS,
		PDBEnabled:         in.PDBEnabled,
		TLSEnabled:         in.TLSEnabled,
		HealthPath:         in.HealthPath,
		ReadinessPath:      in.ReadinessPath,
	}
	if runAs != nil {
		wfInput.Identity = s.identity(*runAs)
	}
	if in.Autoscaling != nil && in.Autoscaling.Enabled {
		wfInput.AutoscalingEnabled = true
		wfInput.AutoscalingMin = in.Autoscaling.MinReplicas
		wfInput.AutoscalingMax = in.Autoscaling.MaxReplicas
		wfInput.AutoscalingTargetCPU = in.Autoscaling.TargetCPUUtilizationPercentage
	}

	_, err = s.Distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: "workload-deploy-" + workload.ID.String()},
		"DeployWorkloadWorkflow", wfInput)

	return workload, err
}

func (s *WorkloadService) Update(ctx context.Context, id string, req domain.UpdateWorkloadRequest) error {
	current, err := s.Repo.GetByID(ctx, uuid.MustParse(id))
	if err != nil {
		return err
	}

	if req.StorageSize != "" && current.StorageSize != "" {
		if utils.ParseStorageToBytes(req.StorageSize) < utils.ParseStorageToBytes(current.StorageSize) {
			return betoerrors.New(betoerrors.CodeInvalidInput, "reduction of storage size is not supported")
		}
	}

	_, err = s.Distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: "workload-update-" + id},
		workflows.DeployWorkloadWorkflow, workflows.DeployWorkloadInput{
			WorkloadID:         id,
			ProjectID:          current.ProjectID.String(),
			Namespace:          current.Namespace,
			ReleaseName:        current.Name,
			Image:              req.Image,
			EnvVars:            req.EnvVars,
			PersistenceEnabled: current.PersistenceEnabled,
			StorageSize:        req.StorageSize,
			Identity:           s.currentIdentity(current),
		})

	return err
}

func (s *WorkloadService) parseCPU(cpu string) int64 {
	res, err := resource.ParseQuantity(cpu)
	if err != nil {
		return 0
	}
	return res.MilliValue() // milli-cpu ( 1 = 1000m)
}

func (s *WorkloadService) parseMemory(mem string) int64 {
	res, err := resource.ParseQuantity(mem)
	if err != nil {
		return 0
	}
	return res.Value() / (1024 * 1024) //  en Mi
}

func (s *WorkloadService) parseStorage(storage string) int64 {
	if storage == "" {
		return 0
	}
	res, err := resource.ParseQuantity(storage)
	if err != nil {
		return 0
	}
	return res.Value() / (1024 * 1024)
}

func (s *WorkloadService) parseStorageToBytes(storage string) int64 {
	res, _ := resource.ParseQuantity(storage)
	return res.Value()
}

func (s *WorkloadService) validateNetwork(port int) error {

	if port <= 0 || port > 65535 {
		return betoerrors.Newf(betoerrors.CodeInvalidInput, "invalid target port: %d (must be between 1 and 65535)", port)
	}
	// Pas de liste de ports "réservés" : le port cible vit dans le network
	// namespace du pod, il ne peut pas entrer en collision avec les services
	// de la plateforme (API, Postgres, Temporal…).
	if port < 1024 && port != 80 {
		return betoerrors.New(betoerrors.CodeInvalidInput, "ports below 1024 (except 80) are system-privileged and not allowed")
	}

	return nil
}

// currentIdentity : identité conservée lors d'une mise à jour.
func (s *WorkloadService) currentIdentity(w *domain.Workload) *activities.WorkloadIdentity {
	if w.ServiceAccountID == nil || s.Identity == nil {
		return nil
	}
	return s.identity(*w.ServiceAccountID)
}

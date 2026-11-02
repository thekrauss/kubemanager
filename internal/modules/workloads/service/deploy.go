package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	buildsDomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	"github.com/thekrauss/kubemanager/internal/modules/builds/registry"
	"strings"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/activities"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/google/uuid"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"gorm.io/gorm"
	"k8s.io/client-go/rest"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	projectRepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
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

// ErrWorkloadNameTaken : le nom sert de release Helm dans l'espace du projet ;
// deux conteneurs du même nom se disputeraient la même release.
var ErrWorkloadNameTaken = errors.New("a container with this name already exists in the project")

type WorkloadService struct {
	// Identity : nil si l'identité des workloads est désactivée.
	Identity    *IdentitySettings
	Distributor temporal.TaskDistributor
	Repo        repository.WorkloadRepository
	ProjectRepo projectRepo.ProjectRepository
	K8sClient   *kubernetes.Clientset
	K8sConfig   *rest.Config
	Logger      *zap.SugaredLogger
	// Registry : registre de la plateforme (nil si builds désactivés).
	Registry *RegistrySettings
	// Variables : variables et secrets chiffrés.
	Variables repository.VariableStore
	// Domains, DomainCfg : domaines personnalisés (nil : indisponibles).
	Domains   repository.DomainStore
	DomainCfg *DomainSettings
}

// RegistrySettings : registre d'images de la plateforme.
type RegistrySettings struct {
	Host string
}

// checkImage : une image du registre de la plateforme n'est déployable que
// par son projet.
func (s *WorkloadService) checkImage(project uuid.UUID, image string) error {
	if s.Registry == nil || !registry.IsPlatformImage(s.Registry.Host, image) || registry.OwnedBy(s.Registry.Host, image, project) {
		return nil
	}
	return buildsDomain.ErrForeignImage
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
	workload, spec, err := s.create(ctx, in, utils.WorkloadStarting, utils.PhaseHelmChartLoading)
	if err != nil {
		return nil, err
	}
	wfInput := s.deployInput(workload, spec)
	_, err = s.Distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: workflows.WorkflowIDDeployPrefix + workload.ID.String()},
		workflows.DeployWorkloadWorkflowName, wfInput)

	return workload, err
}

// CreateAwaitingBuild : mêmes contrôles et même réservation de quota qu'un
// déploiement, mais sans image ni installation : le conteneur attend le build
// qui le déploiera (DeployBuiltImage).
func (s *WorkloadService) CreateAwaitingBuild(ctx context.Context, in *WorkloadServiceRequest) (*domain.Workload, error) {
	in.Image = ""
	workload, _, err := s.create(ctx, in, utils.WorkloadBuilding, utils.PhaseBuildRunning)
	return workload, err
}

// Remove : annule une création dont la suite a échoué (rien n'est installé).
func (s *WorkloadService) Remove(ctx context.Context, id uuid.UUID) error {
	return s.Repo.Delete(ctx, id)
}

// MarkAwaitingBuild : conteneur jamais déployé, nouvelle image en construction.
func (s *WorkloadService) MarkAwaitingBuild(ctx context.Context, w *domain.Workload) error {
	if w.Image != "" {
		return nil
	}
	return s.Repo.UpdateStatus(ctx, w.ID, utils.WorkloadBuilding, utils.PhaseBuildRunning)
}

// DeployBuiltImage : image construite par le serveur (push, « déployer
// maintenant »). Même logique qu'une mise à jour, contrôle de l'image compris ;
// le projet du build doit être celui du conteneur. Un conteneur arrêté n'est
// pas relancé : le déployer le redémarrerait.
func (s *WorkloadService) DeployBuiltImage(ctx context.Context, project, id uuid.UUID, image string) error {
	current, err := s.Repo.GetByID(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return buildsDomain.ErrWorkloadGone
	}
	if err != nil {
		return err
	}
	if current.ProjectID != project {
		return buildsDomain.ErrWorkloadGone
	}
	switch current.Status {
	case utils.WorkloadStopped:
		return buildsDomain.ErrWorkloadStopped
	case utils.WorkloadDeleting, utils.WorkloadDeleted:
		return buildsDomain.ErrWorkloadGone
	}
	err = s.Update(ctx, id.String(), domain.UpdateWorkloadRequest{Image: image})
	if errors.Is(err, domain.ErrDeployInProgress) {
		return buildsDomain.ErrWorkloadBusy
	}
	return err
}

// MarkBuildFailed : la première image n'a pas pu être construite. Un conteneur
// déjà en service n'est pas touché.
func (s *WorkloadService) MarkBuildFailed(ctx context.Context, project, id uuid.UUID) error {
	current, err := s.Repo.GetByID(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil || current.ProjectID != project || current.Image != "" || current.Status == utils.WorkloadDeleting {
		return err
	}
	return s.Repo.UpdateStatus(ctx, id, utils.WorkloadFailed, utils.PhaseBuildFailed)
}

// ensureBuilt : un conteneur sans image n'a encore rien à arrêter, relancer ou
// déployer.
func ensureBuilt(w *domain.Workload) error {
	if w.Image == "" {
		return domain.ErrWorkloadNotBuilt
	}
	return nil
}

func (s *WorkloadService) create(ctx context.Context, in *WorkloadServiceRequest, status, phase string) (*domain.Workload, domain.CreateWorkloadRequest, error) {
	var none domain.CreateWorkloadRequest
	pID, _ := uuid.Parse(in.ProjectID)

	currentCPU, currentMem, currentStorage, _ := s.Repo.GetTotalUsageByProject(ctx, pID)

	project, err := s.ProjectRepo.GetProjectByID(ctx, pID.String())
	if err != nil {
		return nil, none, betoerrors.New(betoerrors.CodeNotFound, "project not found")
	}

	replicas := int64(in.Replicas)
	if replicas <= 0 {
		// Non précisé : le quota comptait déjà 1, le déploiement partait à 0.
		replicas, in.Replicas = 1, 1
	}

	if err := s.validateNetwork(in.TargetPort); err != nil {
		return nil, none, err
	}

	requestedTotalCPU := s.parseCPU(in.CPULimit) * replicas
	limitCPU := s.parseCPU(project.CpuLimit)
	if (currentCPU + requestedTotalCPU) > limitCPU {
		return nil, none, betoerrors.Newf(betoerrors.CodeInvalidInput, "quota exceeded: CPU limit reached (%d/%d m)", currentCPU+requestedTotalCPU, limitCPU)
	}

	requestedTotalMem := s.parseMemory(in.MemoryLimit) * replicas
	limitMem := s.parseMemory(project.MemoryLimit)
	if (currentMem + requestedTotalMem) > limitMem {
		return nil, none, betoerrors.New(betoerrors.CodeInvalidInput, "quota exceeded: Memory limit reached")
	}

	if in.PersistenceEnabled {
		requestedStorage := s.parseStorage(in.StorageSize)
		limitStorage := s.parseStorage(project.StorageLimit)
		if (currentStorage + requestedStorage) > limitStorage {
			return nil, none, betoerrors.New(betoerrors.CodeInvalidInput, "quota exceeded: Storage limit reached")
		}
	}

	_ = currentStorage

	existing, err := s.Repo.ListByProject(ctx, pID)
	if err != nil {
		return nil, none, err
	}
	for _, w := range existing {
		if w.Name == in.Name {
			return nil, none, ErrWorkloadNameTaken
		}
	}

	if err := s.checkImage(pID, in.Image); err != nil {
		return nil, none, err
	}
	variables := domain.VariableChange{Env: in.EnvVars, Secrets: in.SecretData}
	if err := variables.Validate(); err != nil {
		return nil, none, err
	}
	targetNamespace := utils.ProjectNamespace(project.Name)

	// Identité : vérifiée avant toute création (actAs sur le compte).
	var runAs *uuid.UUID
	if in.ServiceAccount != "" {
		if s.Identity == nil {
			return nil, none, ErrWorkloadIdentityDisabled
		}
		saID, err := s.Identity.Authorizer.AuthorizeRunAs(ctx, in.Caller, pID, in.ServiceAccount)
		if err != nil {
			return nil, none, err
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
		StorageClass:       defaultIfEmpty(in.StorageClass, utils.DefaultStorageClass),
		TLSEnabled:         in.TLSEnabled,
		Status:             status,
		CurrentPhase:       phase,
		ServiceAccountID:   runAs,
	}
	spec := specOf(in.CreateWorkloadRequest)
	if runAs != nil {
		spec.ServiceAccount = iam.ServiceAccountPrincipal(*runAs).String()
	}
	if workload.Spec, err = json.Marshal(spec); err != nil {
		return nil, none, err
	}
	workload.ExternalURL = publicRoute(in.Name, pID, in.TLSEnabled).URL

	if err := s.Repo.Create(ctx, workload); err != nil {
		if errors.Is(err, repository.ErrNameTaken) {
			return nil, none, ErrWorkloadNameTaken
		}
		return nil, none, err
	}

	// Valeurs chiffrées avant le workflow, qui ne reçoit que leur révision.
	if workload.EnvRevision, _, err = s.Variables.Apply(ctx, workload.ID, variables); err != nil {
		_ = s.Repo.Delete(ctx, workload.ID)
		return nil, none, err
	}
	return workload, spec, nil
}

// Update : nouvelle version (image, disque, variables). Le reste de la
// configuration est repris du workload enregistré : avant, le workflow était
// relancé avec la seule image, et perdait port, ressources, réplicas, TLS,
// URL publique et variables d'environnement.
func (s *WorkloadService) Update(ctx context.Context, id string, req domain.UpdateWorkloadRequest) error {
	wID, err := uuid.Parse(id)
	if err != nil {
		return betoerrors.New(betoerrors.CodeNotFound, "workload not found")
	}
	current, err := s.Repo.GetByID(ctx, wID)
	if err != nil {
		return err
	}
	if err := s.ensureNoDeployRunning(ctx, current.ID); err != nil {
		return err
	}
	if req.Image == "" {
		if err := ensureBuilt(current); err != nil {
			return err
		}
	}
	variables := domain.VariableChange{Env: req.EnvVars, Secrets: req.SecretData, ReplaceEnv: req.EnvVars != nil}
	if err := variables.Validate(); err != nil {
		return err
	}

	if req.StorageSize != "" && current.StorageSize != "" {
		if utils.ParseStorageToBytes(req.StorageSize) < utils.ParseStorageToBytes(current.StorageSize) {
			return betoerrors.New(betoerrors.CodeInvalidInput, "reduction of storage size is not supported")
		}
	}
	spec, err := s.specFor(current)
	if err != nil {
		return err
	}
	if req.Image != "" {
		if err := s.checkImage(current.ProjectID, req.Image); err != nil {
			return err
		}
		current.Image, spec.Image = req.Image, req.Image
	}
	runtimeChanged, err := s.applyRuntime(ctx, current, &spec, req)
	if err != nil {
		return err
	}
	if req.StorageSize != "" {
		current.StorageSize, spec.StorageSize = req.StorageSize, req.StorageSize
	}

	if current.EnvRevision, _, err = s.applyVariables(ctx, current, variables); err != nil {
		return err
	}

	raw, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	if runtimeChanged {
		if err := s.Repo.UpdateRuntime(ctx, current.ID, current.Replicas, current.CPULimit, current.MemoryLimit, current.TargetPort, current.TLSEnabled); err != nil {
			return err
		}
	}
	url := publicRoute(current.Name, current.ProjectID, spec.TLSEnabled).URL
	if err := s.Repo.UpdateSpec(ctx, current.ID, current.Image, current.StorageSize, url, raw); err != nil {
		return err
	}
	in, err := s.inputFor(ctx, current, spec)
	if err != nil {
		return err
	}
	_, err = s.Distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: workflows.WorkflowIDUpdatePrefix + id},
		workflows.DeployWorkloadWorkflow, in)
	return err
}

// ensureNoDeployRunning : Temporal rattache sans erreur une demande à
// l'exécution en cours du même identifiant : la nouvelle version ne serait
// jamais déployée, alors que la base la donnerait pour acquise.
func (s *WorkloadService) ensureNoDeployRunning(ctx context.Context, id uuid.UUID) error {
	for _, prefix := range []string{workflows.WorkflowIDDeployPrefix, workflows.WorkflowIDUpdatePrefix} {
		running, err := s.Distributor.IsRunning(ctx, prefix+id.String())
		if err != nil {
			return err
		}
		if running {
			return domain.ErrDeployInProgress
		}
	}
	return nil
}

// applyVariables : importe au besoin les variables d'un conteneur antérieur,
// puis applique la modification. Renvoie la révision à déployer.
func (s *WorkloadService) applyVariables(ctx context.Context, w *domain.Workload, change domain.VariableChange) (int64, bool, error) {
	if w.EnvRevision == 0 {
		legacy, err := s.currentEnv(ctx, w)
		if err != nil {
			return 0, false, err
		}
		if _, err := s.Variables.Import(ctx, w.ID, legacy); err != nil {
			return 0, false, err
		}
	}
	return s.Variables.Apply(ctx, w.ID, change)
}

// ListVariables : variables du conteneur, sans la valeur des secrets.
func (s *WorkloadService) ListVariables(ctx context.Context, id uuid.UUID) ([]domain.VariableView, error) {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.EnvRevision == 0 {
		legacy, err := s.currentEnv(ctx, w)
		if err != nil {
			return nil, err
		}
		if _, err := s.Variables.Import(ctx, w.ID, legacy); err != nil {
			return nil, err
		}
	}
	return s.Variables.List(ctx, id)
}

// PatchVariables : applique les modifications et redéploie si une valeur a
// changé (nouveaux pods). Un conteneur arrêté est refusé : le redéployer le
// redémarrerait.
func (s *WorkloadService) PatchVariables(ctx context.Context, id uuid.UUID, req domain.PatchVariablesRequest) (*domain.VariablesResponse, error) {
	current, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.Status == utils.WorkloadStopped {
		return nil, domain.ErrWorkloadStopped
	}
	if err := s.ensureNoDeployRunning(ctx, id); err != nil {
		return nil, err
	}
	change := domain.VariableChange{Env: req.Env, Secrets: req.Secrets, Unset: req.Unset}
	if err := change.Validate(); err != nil {
		return nil, err
	}
	var changed bool
	if current.EnvRevision, changed, err = s.applyVariables(ctx, current, change); err != nil {
		return nil, err
	}
	// Sans image : la valeur sera prise en compte au premier déploiement.
	if changed && current.Image != "" {
		if err := s.redeploy(ctx, current); err != nil {
			return nil, err
		}
	}
	views, err := s.Variables.List(ctx, id)
	if err != nil {
		return nil, err
	}
	return &domain.VariablesResponse{Variables: views, Changed: changed}, nil
}

// specOf : spec déclarative enregistrée (sans variables ni secrets, qui
// restent dans le Secret Kubernetes du conteneur).
func specOf(req domain.CreateWorkloadRequest) domain.CreateWorkloadRequest {
	req.EnvVars, req.SecretData = nil, nil
	return req
}

// specFor : spec enregistrée ; conteneur antérieur à la spec : reconstituée
// depuis ses colonnes.
func (s *WorkloadService) specFor(w *domain.Workload) (domain.CreateWorkloadRequest, error) {
	var spec domain.CreateWorkloadRequest
	if len(w.Spec) > 0 {
		if err := json.Unmarshal(w.Spec, &spec); err != nil {
			return spec, fmt.Errorf("workload spec: %w", err)
		}
		return spec, nil
	}
	return domain.CreateWorkloadRequest{ProjectID: w.ProjectID.String(), Name: w.Name, Image: w.Image, Replicas: w.Replicas,
		CPURequest: w.CPURequest, MemoryRequest: w.MemoryRequest, CPULimit: w.CPULimit, MemoryLimit: w.MemoryLimit,
		TargetPort: w.TargetPort, PersistenceEnabled: w.PersistenceEnabled, StorageSize: w.StorageSize,
		StorageClass: w.StorageClass, TLSEnabled: w.TLSEnabled}, nil
}

// applyRuntime : réplicas, limites, port, TLS et disque demandés, vérifiés
// contre les quotas du projet : une mise à jour ne doit pas permettre de
// dépasser ce qu'une création refuserait.
func (s *WorkloadService) applyRuntime(ctx context.Context, w *domain.Workload, spec *domain.CreateWorkloadRequest, req domain.UpdateWorkloadRequest) (bool, error) {
	if req.TargetPort != 0 {
		if err := s.validateNetwork(req.TargetPort); err != nil {
			return false, err
		}
	}
	replicas, cpu, mem := int64(max(w.Replicas, 1)), w.CPULimit, w.MemoryLimit
	newReplicas, newCPU, newMem := replicas, cpu, mem
	if req.Replicas < 0 {
		return false, betoerrors.New(betoerrors.CodeInvalidInput, "replicas must be positive")
	}
	if req.Replicas > 0 {
		newReplicas = int64(req.Replicas)
	}
	// Une valeur illisible compterait pour 0 dans le quota.
	if req.CPULimit != "" {
		if _, err := resource.ParseQuantity(req.CPULimit); err != nil {
			return false, betoerrors.Newf(betoerrors.CodeInvalidInput, "invalid CPU limit: %s", req.CPULimit)
		}
		newCPU = req.CPULimit
	}
	if req.MemoryLimit != "" {
		if _, err := resource.ParseQuantity(req.MemoryLimit); err != nil {
			return false, betoerrors.Newf(betoerrors.CodeInvalidInput, "invalid memory limit: %s", req.MemoryLimit)
		}
		newMem = req.MemoryLimit
	}
	growsDisk := req.StorageSize != "" && w.PersistenceEnabled && req.StorageSize != w.StorageSize
	if newReplicas != replicas || newCPU != cpu || newMem != mem || growsDisk {
		usedCPU, usedMem, usedStorage, err := s.Repo.GetTotalUsageByProject(ctx, w.ProjectID)
		if err != nil {
			return false, err
		}
		project, err := s.ProjectRepo.GetProjectByID(ctx, w.ProjectID.String())
		if err != nil {
			return false, betoerrors.New(betoerrors.CodeNotFound, "project not found")
		}
		totalCPU := usedCPU - s.parseCPU(cpu)*replicas + s.parseCPU(newCPU)*newReplicas
		if limit := s.parseCPU(project.CpuLimit); totalCPU > limit {
			return false, betoerrors.Newf(betoerrors.CodeInvalidInput, "quota exceeded: CPU limit reached (%d/%d m)", totalCPU, limit)
		}
		if usedMem-s.parseMemory(mem)*replicas+s.parseMemory(newMem)*newReplicas > s.parseMemory(project.MemoryLimit) {
			return false, betoerrors.New(betoerrors.CodeInvalidInput, "quota exceeded: Memory limit reached")
		}
		if growsDisk && usedStorage-s.parseStorage(w.StorageSize)+s.parseStorage(req.StorageSize) > s.parseStorage(project.StorageLimit) {
			return false, betoerrors.New(betoerrors.CodeInvalidInput, "quota exceeded: Storage limit reached")
		}
	}
	changed := newReplicas != replicas || newCPU != cpu || newMem != mem
	if newReplicas != replicas {
		w.Replicas, spec.Replicas = int(newReplicas), int(newReplicas)
	}
	if newCPU != cpu {
		w.CPULimit, spec.CPULimit = newCPU, newCPU
	}
	if newMem != mem {
		w.MemoryLimit, spec.MemoryLimit = newMem, newMem
	}
	if req.TargetPort != 0 && req.TargetPort != w.TargetPort {
		w.TargetPort, spec.TargetPort, changed = req.TargetPort, req.TargetPort, true
	}
	if req.TLSEnabled != nil && *req.TLSEnabled != w.TLSEnabled {
		w.TLSEnabled, spec.TLSEnabled, changed = *req.TLSEnabled, *req.TLSEnabled, true
	}
	return changed, nil
}

// deployInput : entrée du workflow de déploiement, construite de la même
// façon à la création et à chaque mise à jour.
func (s *WorkloadService) deployInput(w *domain.Workload, spec domain.CreateWorkloadRequest) workflows.DeployWorkloadInput {
	route := publicRoute(w.Name, w.ProjectID, spec.TLSEnabled)
	in := workflows.DeployWorkloadInput{
		WorkloadID: w.ID.String(), ProjectID: w.ProjectID.String(), Namespace: w.Namespace, ReleaseName: w.Name,
		Image: w.Image, EnvRevision: w.EnvRevision, Replicas: spec.Replicas,
		PersistenceEnabled: spec.PersistenceEnabled, StorageSize: w.StorageSize, StorageClass: spec.StorageClass,
		MountPath: spec.MountPath, TargetPort: spec.TargetPort, ServiceType: spec.ServiceType,
		CPURequest: spec.CPURequest, MemoryRequest: spec.MemoryRequest, CPULimit: spec.CPULimit, MemoryLimit: spec.MemoryLimit,
		RunAsUser: spec.RunAsUser, RunAsGroup: spec.RunAsGroup, ReadOnlyRootFS: spec.ReadOnlyRootFS,
		PDBEnabled: spec.PDBEnabled, TLSEnabled: spec.TLSEnabled, HealthPath: spec.HealthPath, ReadinessPath: spec.ReadinessPath,
		ExternalHost: route.Host, IngressClass: route.IngressClass, IngressNamespace: route.IngressNamespace,
		Identity:      s.currentIdentity(w),
		PlatformImage: s.Registry != nil && registry.IsPlatformImage(s.Registry.Host, w.Image),
	}
	if a := spec.Autoscaling; a != nil && a.Enabled {
		in.AutoscalingEnabled, in.AutoscalingMin, in.AutoscalingMax = true, a.MinReplicas, a.MaxReplicas
		in.AutoscalingTargetCPU = a.TargetCPUUtilizationPercentage
	}
	return in
}

// currentEnv : variables d'environnement en place (secret <nom>-env créé au
// déploiement) ; aucune si le secret n'existe pas.
func (s *WorkloadService) currentEnv(ctx context.Context, w *domain.Workload) (map[string]string, error) {
	if s.K8sClient == nil {
		return nil, nil
	}
	secret, err := s.K8sClient.CoreV1().Secrets(w.Namespace).Get(ctx, utils.EnvSecretName(w.Name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read current environment: %w", err)
	}
	env := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		env[k] = string(v)
	}
	return env, nil
}

// route : exposition publique d'un workload.
type route struct {
	Host             string // vide : pas d'ingress
	URL              string // enregistrée avec le workload, affichée à l'utilisateur
	IngressClass     string
	IngressNamespace string
}

// publicRoute : <nom>-<projet>.<ip>.sslip.io si l'IP du point d'entrée est
// configurée (vps.vps_ip) ; sinon aucune URL publique (avant : hôte
// « web-xxxxxx..sslip.io » invalide, refusé par Kubernetes).
func publicRoute(name string, projectID uuid.UUID, tls bool) route {
	r := route{
		IngressClass:     configs.AppConfig.Kubernetes.IngressClass,
		IngressNamespace: configs.AppConfig.Kubernetes.IngressNamespace,
	}
	ip := strings.TrimSpace(configs.AppConfig.Vps.AdressIp)
	if ip == "" {
		return r
	}
	r.Host = fmt.Sprintf("%s-%s.%s.%s", name, projectID.String()[:publicRouteProjectIDLen], ip, publicRouteDomain)
	scheme := schemeHTTP
	if tls {
		scheme = schemeHTTPS
	}
	r.URL = scheme + "://" + r.Host
	return r
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
	return res.Value() / utils.BytesPerMi //  en Mi
}

func (s *WorkloadService) parseStorage(storage string) int64 {
	if storage == "" {
		return 0
	}
	res, err := resource.ParseQuantity(storage)
	if err != nil {
		return 0
	}
	return res.Value() / utils.BytesPerMi
}

func (s *WorkloadService) parseStorageToBytes(storage string) int64 {
	res, _ := resource.ParseQuantity(storage)
	return res.Value()
}

func (s *WorkloadService) validateNetwork(port int) error {

	if port <= 0 || port > maxTargetPort {
		return betoerrors.Newf(betoerrors.CodeInvalidInput, "invalid target port: %d (must be between 1 and 65535)", port)
	}
	// Pas de liste de ports "réservés" : le port cible vit dans le network
	// namespace du pod, il ne peut pas entrer en collision avec les services
	// de la plateforme (API, Postgres, Temporal…).
	if port < privilegedPortLimit && port != httpPort {
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

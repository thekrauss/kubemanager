package service

import (
	"context"
	"errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	typesproject "github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"github.com/thekrauss/kubemanager/internal/modules/projects/workflows"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	workloadRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
)

type IProjectService interface {
	CreateProject(ctx context.Context, req typesproject.CreateProjectRequest, ownerID string) (*typesproject.ProjectResponse, error)
	DeleteProject(ctx context.Context, projectID string) (*typesproject.ProjectResponse, error)
	GetProjectStatus(ctx context.Context, projectID string) (*typesproject.ProjectStatusResponse, error)
	GetMetrics(ctx context.Context, projectID string) (*typesproject.NamespaceMetrics, error)
}

var _ IProjectService = (*ProjectService)(nil)

type ProjectService struct {
	Distributor  temporal.TaskDistributor
	Config       *configs.GlobalConfig
	Logger       *zap.SugaredLogger
	Repos        repository.ProjectRepository
	K8sClient    *kubernetes.Clientset
	K8sConfig    *rest.Config
	WorkloadRepo workloadRepo.WorkloadRepository
}

func NewProjectService(
	dist temporal.TaskDistributor,
	cfg *configs.GlobalConfig,
	log *zap.SugaredLogger,
	repo repository.ProjectRepository,
	k8s *kubernetes.Clientset,
	k8sCfg *rest.Config,
	workloadRepo workloadRepo.WorkloadRepository,
) IProjectService {
	return &ProjectService{
		Distributor:  dist,
		Config:       cfg,
		Logger:       log.With("service", "ProjectService"),
		Repos:        repo,
		K8sClient:    k8s,
		K8sConfig:    k8sCfg,
		WorkloadRepo: workloadRepo,
	}
}

// Limites par défaut d'un projet (identiques aux défauts de la table projects).
const (
	DefaultProjectCPU    = "2000m"
	DefaultProjectMemory = "4Gi"
)

// ErrInvalidProjectLimits : limite CPU ou mémoire illisible.
var ErrInvalidProjectLimits = errors.New("invalid project limits: use Kubernetes quantities (e.g. 2000m, 4Gi)")

func (s *ProjectService) CreateProject(ctx context.Context, req typesproject.CreateProjectRequest, ownerID string) (*typesproject.ProjectResponse, error) {
	// Sans limites fournies (inscription), les défauts du projet ; une valeur
	// illisible est refusée ici plutôt qu'au milieu du workflow.
	if strings.TrimSpace(req.CpuLimit) == "" {
		req.CpuLimit = DefaultProjectCPU
	}
	if strings.TrimSpace(req.MemoryLimit) == "" {
		req.MemoryLimit = DefaultProjectMemory
	}
	if _, err := resource.ParseQuantity(req.CpuLimit); err != nil {
		return nil, ErrInvalidProjectLimits
	}
	if _, err := resource.ParseQuantity(req.MemoryLimit); err != nil {
		return nil, ErrInvalidProjectLimits
	}
	// Identifiant choisi ici : l'appelant (bootstrap, console) le reçoit tout
	// de suite, alors que le workflow ne crée la ligne qu'ensuite.
	projectID := uuid.New().String()
	input := workflows.CreateProjectInput{
		ProjectID:   projectID,
		Name:        req.Name,
		Description: req.Description,
		OwnerID:     ownerID,
		CpuLimit:    req.CpuLimit,
		MemoryLimit: req.MemoryLimit,
	}

	handle, err := s.Distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: "project-create-" + projectID},
		workflows.CreateProjectWorkflow, input,
	)
	if err != nil {
		s.Logger.Errorw("Failed to start project workflow", "error", err)
		return nil, err
	}

	return &typesproject.ProjectResponse{
		ProjectID:  projectID,
		WorkflowID: handle.WorkflowID,
		Status:     utils.ProjectStatusProvisioning,
		Message:    "the project is currently being created on the cluster.",
	}, nil
}

func (s *ProjectService) DeleteProject(ctx context.Context, projectID string) (*typesproject.ProjectResponse, error) {
	project, err := s.Repos.GetProjectByID(ctx, projectID)
	if err != nil {
		s.Logger.Errorw("Project not found for deletion", "projectID", projectID, "error", err)
		return nil, err
	}

	handle, err := s.Distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: "project-delete-" + projectID},
		"DeleteProjectWorkflow", projectID, project.Name,
	)
	if err != nil {
		s.Logger.Errorw("Failed to start delete workflow", "error", err)
		return nil, err
	}

	return &typesproject.ProjectResponse{
		ProjectID:  projectID,
		WorkflowID: handle.WorkflowID,
		Status:     "DELETING",
		Message:    "The project deletion and cleanup has been initiated.",
	}, nil
}

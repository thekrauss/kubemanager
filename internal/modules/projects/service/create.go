package service

import (
	"context"

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

func (s *ProjectService) CreateProject(ctx context.Context, req typesproject.CreateProjectRequest, ownerID string) (*typesproject.ProjectResponse, error) {
	input := workflows.CreateProjectInput{
		Name:        req.Name,
		Description: req.Description,
		OwnerID:     ownerID,
		CpuLimit:    req.CpuLimit,
		MemoryLimit: req.MemoryLimit,
	}

	handle, err := s.Distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: "project-create-" + uuid.New().String()},
		workflows.CreateProjectWorkflow, input,
	)
	if err != nil {
		s.Logger.Errorw("Failed to start project workflow", "error", err)
		return nil, err
	}

	return &typesproject.ProjectResponse{
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

package service

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"github.com/thekrauss/kubemanager/internal/modules/projects/workflows"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	workloadRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"

	"go.temporal.io/sdk/client"
)

type IProjectService interface {
	CreateProject(ctx context.Context, req domain.CreateProjectRequest, ownerID string) (*domain.ProjectResponse, error)
	DeleteProject(ctx context.Context, projectID string) (*domain.ProjectResponse, error)
	GetProjectStatus(ctx context.Context, projectID string) (*domain.ProjectStatusResponse, error)
	GetMetrics(ctx context.Context, projectID string) (*domain.NamespaceMetrics, error)
}

var _ IProjectService = (*ProjectService)(nil)

type ProjectService struct {
	TemporalClient client.Client
	Config         *configs.GlobalConfig
	Logger         *zap.SugaredLogger
	Repos          repository.ProjectRepository
	K8sClient      *kubernetes.Clientset
	K8sConfig      *rest.Config
	WorkloadRepo   workloadRepo.WorkloadRepository
}

func NewProjectService(
	tc client.Client,
	cfg *configs.GlobalConfig,
	log *zap.SugaredLogger,
	repo repository.ProjectRepository,
	k8s *kubernetes.Clientset,
) IProjectService {
	return &ProjectService{
		TemporalClient: tc,
		Config:         cfg,
		Logger:         log.With("service", "ProjectService"),
		Repos:          repo,
		K8sClient:      k8s,
	}
}
func (s *ProjectService) CreateProject(ctx context.Context, req domain.CreateProjectRequest, ownerID string) (*domain.ProjectResponse, error) {
	workflowID := "project-create-" + uuid.New().String()

	workflowOptions := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: s.Config.Temporal.TaskQueue,
	}

	input := workflows.CreateProjectInput{
		Name:        req.Name,
		Description: req.Description,
		OwnerID:     ownerID,
		CpuLimit:    req.CpuLimit,
		MemoryLimit: req.MemoryLimit,
	}

	we, err := s.TemporalClient.ExecuteWorkflow(ctx, workflowOptions, workflows.CreateProjectWorkflow, input)
	if err != nil {
		s.Logger.Errorw("Failed to start project workflow", "error", err)
		return nil, err
	}

	return &domain.ProjectResponse{
		WorkflowID: we.GetID(),
		Status:     utils.ProjectStatusProvisioning,
		Message:    "the project is currently being created on the cluster.",
	}, nil
}

func (s *ProjectService) DeleteProject(ctx context.Context, projectID string) (*domain.ProjectResponse, error) {
	project, err := s.Repos.GetProjectByID(ctx, projectID)
	if err != nil {
		s.Logger.Errorw("Project not found for deletion", "projectID", projectID, "error", err)
		return nil, err
	}

	workflowID := "project-delete-" + projectID
	workflowOptions := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: s.Config.Temporal.TaskQueue,
	}

	we, err := s.TemporalClient.ExecuteWorkflow(ctx, workflowOptions, "DeleteProjectWorkflow", projectID, project.Name)
	if err != nil {
		s.Logger.Errorw("Failed to start delete workflow", "error", err)
		return nil, err
	}

	return &domain.ProjectResponse{
		ProjectID:  projectID,
		WorkflowID: we.GetID(),
		Status:     "DELETING",
		Message:    "The project deletion and cleanup has been initiated.",
	}, nil
}

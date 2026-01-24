package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"k8s.io/apimachinery/pkg/api/resource"

	projectRepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
	"go.temporal.io/sdk/client"
)

type WorkloadService struct {
	TemporalClient client.Client
	Repo           repository.WorkloadRepository
	ProjectRepo    projectRepo.ProjectRepository
}

func NewWorkloadService(
	temporal client.Client,
	repo repository.WorkloadRepository,
	pRepo projectRepo.ProjectRepository,
) *WorkloadService {
	return &WorkloadService{
		TemporalClient: temporal,
		Repo:           repo,
		ProjectRepo:    pRepo,
	}
}

type WorkloadServiceRequest struct {
	domain.CreateWorkloadRequest
}

func (s *WorkloadService) DeployNewWorkload(ctx context.Context, in *WorkloadServiceRequest) (*domain.Workload, error) {
	pID, _ := uuid.Parse(in.ProjectID)

	currentCPU, currentMem, currentStorage, _ := s.Repo.GetTotalUsageByProject(ctx, pID)

	project, err := s.ProjectRepo.GetProjectByID(ctx, pID.String())
	if err != nil {
		return nil, fmt.Errorf("project not found: %w", err)
	}

	replicas := int64(in.Replicas)
	if replicas <= 0 {
		replicas = 1
	}

	requestedTotalCPU := s.parseCPU(in.CPULimit) * replicas
	limitCPU := s.parseCPU(project.CpuLimit)
	if (currentCPU + requestedTotalCPU) > limitCPU {
		return nil, fmt.Errorf("quota exceeded: CPU limit reached (%d/%d m)", currentCPU+requestedTotalCPU, limitCPU)
	}

	requestedTotalMem := s.parseMemory(in.MemoryLimit) * replicas
	limitMem := s.parseMemory(project.MemoryLimit)
	if (currentMem + requestedTotalMem) > limitMem {
		return nil, fmt.Errorf("quota exceeded: Memory limit reached")
	}

	if in.PersistenceEnabled {
		requestedStorage := s.parseStorage(in.StorageSize)
		limitStorage := s.parseStorage(project.StorageLimit)
		if (currentStorage + requestedStorage) > limitStorage {
			return nil, fmt.Errorf("quota exceeded: Storage limit reached")
		}
	}

	_ = currentStorage

	targetNamespace := fmt.Sprintf("km-%s", project.Name)

	workload := &domain.Workload{
		ID:          uuid.New(),
		ProjectID:   pID,
		Name:        in.Name,
		Namespace:   targetNamespace,
		Image:       in.Image,
		CPULimit:    in.CPULimit,
		MemoryLimit: in.MemoryLimit,
		Status:      "STARTING",
	}

	if err := s.Repo.Create(ctx, workload); err != nil {
		return nil, err
	}
	workflowOptions := client.StartWorkflowOptions{
		ID:        "workload-deploy-" + workload.ID.String(),
		TaskQueue: "kubemanager-tasks",
	}

	_, err = s.TemporalClient.ExecuteWorkflow(ctx, workflowOptions, "DeployWorkloadWorkflow", workflows.DeployWorkloadInput{
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
	})

	return workload, err
}

func (s *WorkloadService) UpdateWorkload(ctx context.Context, id string, req domain.UpdateWorkloadRequest) error {
	current, err := s.Repo.GetByID(ctx, uuid.MustParse(id))
	if err != nil {
		return err
	}

	if req.StorageSize != "" && current.StorageSize != "" {
		if utils.ParseStorageToBytes(req.StorageSize) < utils.ParseStorageToBytes(current.StorageSize) {
			return fmt.Errorf("reduction of storage size is not supported")
		}
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:        "workload-update-" + id,
		TaskQueue: "kubemanager-tasks",
	}

	_, err = s.TemporalClient.ExecuteWorkflow(ctx, workflowOptions, workflows.DeployWorkloadWorkflow, workflows.DeployWorkloadInput{
		WorkloadID:         id,
		ProjectID:          current.ProjectID.String(),
		Namespace:          current.Namespace,
		ReleaseName:        current.Name,
		Image:              req.Image,
		EnvVars:            req.EnvVars,
		PersistenceEnabled: current.PersistenceEnabled,
		StorageSize:        req.StorageSize,
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

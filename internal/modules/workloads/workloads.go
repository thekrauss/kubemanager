package http

import (
	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

type WorkloadController struct {
	WorkloadService *service.WorkloadService
}

func NewWorkloadHandler(svc *service.WorkloadService) *WorkloadController {
	return &WorkloadController{WorkloadService: svc}
}

func (h *WorkloadController) CreateWorkload(c *gin.Context, in *domain.CreateWorkloadRequest) (*domain.WorkloadResponse, error) {

	req := &service.WorkloadServiceRequest{
		CreateWorkloadRequest: *in,
	}
	workload, err := h.WorkloadService.DeployNewWorkload(
		c,
		req,
	)

	if err != nil {
		return nil, err
	}

	return &domain.WorkloadResponse{
		WorkloadID: workload.ID.String(),
		Status:     workload.Status,
		Namespace:  workload.Namespace,
		Message:    "Deployment initiated successfully",
	}, nil
}

type GetWorkloadRequest struct {
	ID string `path:"id" desc:"ID du workload"`
}

// (STARTING -> RUNNING)
func (h *WorkloadController) GetWorkloadStatus(c *gin.Context, in *GetWorkloadRequest) (*domain.WorkloadStatusResponse, error) {

	return nil, nil
}

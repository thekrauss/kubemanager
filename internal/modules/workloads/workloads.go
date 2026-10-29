package workloads

import (
	"io"
	"net/http"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

type IWorkloadController interface {
	// CRUD
	CreateWorkload(c *gin.Context, in *domain.CreateWorkloadRequest) (*domain.WorkloadResponse, error)
	GetWorkload(c *gin.Context, in *WorkloadIDRequest) (*domain.Workload, error)
	ListWorkloads(c *gin.Context, in *ListWorkloadsRequest) ([]domain.Workload, error)
	UpdateWorkload(c *gin.Context, in *UpdateWorkloadParams) error
	DeleteWorkload(c *gin.Context, in *WorkloadIDRequest) error

	// Lifecycle K8s
	StopWorkload(c *gin.Context, in *WorkloadIDRequest) error
	StartWorkload(c *gin.Context, in *WorkloadIDRequest) error
	RestartWorkload(c *gin.Context, in *WorkloadIDRequest) error
	RollbackWorkload(c *gin.Context, in *RollbackRequest) error

	// Observabilité
	GetWorkloadStatus(c *gin.Context, in *WorkloadIDRequest) (*domain.LiveStatus, error)
	GetWorkloadLogs(c *gin.Context, in *LogsRequest)
	GetWorkloadEvents(c *gin.Context, in *WorkloadIDRequest) ([]domain.WorkloadEvent, error)
	GetWorkloadMetrics(c *gin.Context, in *WorkloadIDRequest) (*domain.WorkloadMetrics, error)
}

type WorkloadController struct {
	WorkloadService service.IWorkloadService
}

func NewWorkloadHandler(svc service.IWorkloadService) *WorkloadController {
	return &WorkloadController{WorkloadService: svc}
}

//  DTOs d'entrée

type WorkloadIDRequest struct {
	ID string `path:"id" binding:"required" desc:"ID du workload"`
}

type ListWorkloadsRequest struct {
	ProjectID string `query:"project_id" binding:"required" desc:"ID du projet parent"`
}

type UpdateWorkloadParams struct {
	ID   string                       `path:"id" binding:"required"`
	Body domain.UpdateWorkloadRequest `json:",inline"`
}

type RollbackRequest struct {
	ID       string `path:"id" binding:"required"`
	Revision int    `json:"revision" desc:"Révision Helm cible (0 = précédente)"`
}

type LogsRequest struct {
	ID           string `path:"id" binding:"required"`
	Follow       bool   `query:"follow"`
	Previous     bool   `query:"previous"`
	TailLines    int64  `query:"tail"       desc:"Nombre de lignes depuis la fin (default 500)"`
	SinceSeconds int64  `query:"since"      desc:"Lookback en secondes (default 24h)"`
	Timestamps   bool   `query:"timestamps"`
}

//  CRUD

func (h *WorkloadController) CreateWorkload(c *gin.Context, in *domain.CreateWorkloadRequest) (*domain.WorkloadResponse, error) {
	workload, err := h.WorkloadService.Deploy(c.Request.Context(), &service.WorkloadServiceRequest{
		CreateWorkloadRequest: *in,
	})
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

func (h *WorkloadController) GetWorkload(c *gin.Context, in *WorkloadIDRequest) (*domain.Workload, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.Get(c.Request.Context(), id)
}

func (h *WorkloadController) ListWorkloads(c *gin.Context, in *ListWorkloadsRequest) ([]domain.Workload, error) {
	projectID, err := uuid.Parse(in.ProjectID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid project id")
	}
	return h.WorkloadService.List(c.Request.Context(), projectID)
}

func (h *WorkloadController) UpdateWorkload(c *gin.Context, in *UpdateWorkloadParams) error {
	return h.WorkloadService.Update(c.Request.Context(), in.ID, in.Body)
}

func (h *WorkloadController) DeleteWorkload(c *gin.Context, in *WorkloadIDRequest) error {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.Delete(c.Request.Context(), id)
}

//  Lifecycle

func (h *WorkloadController) StopWorkload(c *gin.Context, in *WorkloadIDRequest) error {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.Stop(c.Request.Context(), id)
}

func (h *WorkloadController) StartWorkload(c *gin.Context, in *WorkloadIDRequest) error {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.Start(c.Request.Context(), id)
}

func (h *WorkloadController) RestartWorkload(c *gin.Context, in *WorkloadIDRequest) error {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.Restart(c.Request.Context(), id)
}

func (h *WorkloadController) RollbackWorkload(c *gin.Context, in *RollbackRequest) error {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.Rollback(c.Request.Context(), id, in.Revision)
}

//  Observabilité

// GetWorkloadStatus : fix A6 — stub remplacé par un merge DB + K8s runtime.
func (h *WorkloadController) GetWorkloadStatus(c *gin.Context, in *WorkloadIDRequest) (*domain.LiveStatus, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.LiveStatus(c.Request.Context(), id)
}

// GetWorkloadLogs : stream via HTTP chunked. Ne retourne rien via tonic
// — on écrit directement sur c.Writer (pattern Gin natif pour streaming).
func (h *WorkloadController) GetWorkloadLogs(c *gin.Context, in *LogsRequest) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workload id"})
		return
	}
	opts := service.LogOptions{
		Follow:       in.Follow,
		Previous:     in.Previous,
		TailLines:    in.TailLines,
		SinceSeconds: in.SinceSeconds,
		Timestamps:   in.Timestamps,
	}
	if opts.TailLines == 0 && opts.SinceSeconds == 0 {
		opts = service.DefaultLogOptions()
	}

	stream, err := h.WorkloadService.Logs(c.Request.Context(), id, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer stream.Close()

	c.Writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(c.Writer, stream)
}

func (h *WorkloadController) GetWorkloadEvents(c *gin.Context, in *WorkloadIDRequest) ([]domain.WorkloadEvent, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.Events(c.Request.Context(), id)
}

func (h *WorkloadController) GetWorkloadMetrics(c *gin.Context, in *WorkloadIDRequest) (*domain.WorkloadMetrics, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid workload id")
	}
	return h.WorkloadService.Metrics(c.Request.Context(), id)
}

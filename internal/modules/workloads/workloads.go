package workloads

import (
	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
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
	ListVariables(c *gin.Context, in *WorkloadIDRequest) (*domain.VariablesResponse, error)
	ListDomains(c *gin.Context, in *WorkloadIDRequest) ([]domain.DomainResponse, error)
	AddDomain(c *gin.Context, in *AddDomainParams) (*domain.DomainResponse, error)
	VerifyDomain(c *gin.Context, in *DomainPath) (*domain.DomainResponse, error)
	RemoveDomain(c *gin.Context, in *DomainPath) error
	PatchVariables(c *gin.Context, in *PatchVariablesParams) (*domain.VariablesResponse, error)
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
	ID string `path:"id" binding:"required" description:"ID du conteneur"`
}

type ListWorkloadsRequest struct {
	ProjectID string `query:"project_id" binding:"required" description:"ID du projet parent"`
}

// UpdateWorkloadParams : corps embarqué (encoding/json ignore « inline » :
// avec un champ Body nommé, le corps n'était jamais lu).
type UpdateWorkloadParams struct {
	ID string `path:"id" binding:"required"`
	domain.UpdateWorkloadRequest
}

// PatchVariablesParams : corps embarqué, comme UpdateWorkloadParams.
type PatchVariablesParams struct {
	ID string `path:"id" binding:"required"`
	domain.PatchVariablesRequest
}

// AddDomainParams : corps embarqué.
type AddDomainParams struct {
	ID string `path:"id" binding:"required"`
	domain.AddDomainRequest
}

// DomainPath : un domaine d'un conteneur.
type DomainPath struct {
	ID       string `path:"id" binding:"required"`
	DomainID string `path:"domainID" binding:"required"`
}

type RollbackRequest struct {
	ID       string `path:"id" binding:"required"`
	Revision int    `json:"revision" description:"Révision cible (0 = la précédente)"`
}

type LogsRequest struct {
	ID           string `path:"id" binding:"required"`
	Follow       bool   `query:"follow"`
	Previous     bool   `query:"previous"`
	TailLines    int64  `query:"tail"       description:"Nombre de lignes depuis la fin (défaut : 500)"`
	SinceSeconds int64  `query:"since"      description:"Période à remonter, en secondes (défaut : 24 h)"`
	Timestamps   bool   `query:"timestamps"`
}

//  CRUD

func (h *WorkloadController) CreateWorkload(c *gin.Context, in *domain.CreateWorkloadRequest) (*domain.WorkloadResponse, error) {
	caller, _ := security.PrincipalFromContext(c)
	workload, err := h.WorkloadService.Deploy(c.Request.Context(), &service.WorkloadServiceRequest{
		CreateWorkloadRequest: *in,
		Caller:                caller,
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

func parseWorkload(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, betoerrors.New(betoerrors.CodeNotFound, "workload not found")
	}
	return id, nil
}

func parseDomain(in *DomainPath) (uuid.UUID, uuid.UUID, error) {
	w, err := parseWorkload(in.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	d, err := uuid.Parse(in.DomainID)
	if err != nil {
		return uuid.Nil, uuid.Nil, domain.ErrDomainNotFound
	}
	return w, d, nil
}

// ListDomains : domaines et enregistrements DNS à créer.
func (h *WorkloadController) ListDomains(c *gin.Context, in *WorkloadIDRequest) ([]domain.DomainResponse, error) {
	id, err := parseWorkload(in.ID)
	if err != nil {
		return nil, err
	}
	return h.WorkloadService.ListDomains(c.Request.Context(), id)
}

// AddDomain : nouvelle demande, sans effet sur le routage.
func (h *WorkloadController) AddDomain(c *gin.Context, in *AddDomainParams) (*domain.DomainResponse, error) {
	id, err := parseWorkload(in.ID)
	if err != nil {
		return nil, err
	}
	return h.WorkloadService.AddDomain(c.Request.Context(), id, in.Hostname)
}

// VerifyDomain : vérifie la preuve TXT et, si elle est là, route le domaine.
func (h *WorkloadController) VerifyDomain(c *gin.Context, in *DomainPath) (*domain.DomainResponse, error) {
	w, d, err := parseDomain(in)
	if err != nil {
		return nil, err
	}
	return h.WorkloadService.VerifyDomain(c.Request.Context(), w, d)
}

// RemoveDomain : retire le domaine et sa route.
func (h *WorkloadController) RemoveDomain(c *gin.Context, in *DomainPath) error {
	w, d, err := parseDomain(in)
	if err != nil {
		return err
	}
	return h.WorkloadService.RemoveDomain(c.Request.Context(), w, d)
}

// ListVariables : variables et secrets, sans la valeur des secrets.
func (h *WorkloadController) ListVariables(c *gin.Context, in *WorkloadIDRequest) (*domain.VariablesResponse, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeNotFound, "workload not found")
	}
	views, err := h.WorkloadService.ListVariables(c.Request.Context(), id)
	if err != nil {
		return nil, err
	}
	return &domain.VariablesResponse{Variables: views}, nil
}

// PatchVariables : modifie variables et secrets, et redéploie si besoin.
func (h *WorkloadController) PatchVariables(c *gin.Context, in *PatchVariablesParams) (*domain.VariablesResponse, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeNotFound, "workload not found")
	}
	return h.WorkloadService.PatchVariables(c.Request.Context(), id, in.PatchVariablesRequest)
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
	return h.WorkloadService.Update(c.Request.Context(), in.ID, in.UpdateWorkloadRequest)
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

	c.Writer.Header().Set(httpheader.ContentType, httpheader.MIMETextPlainUTF8)
	c.Writer.Header().Set(httpheader.CacheControl, httpheader.NoCache)
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

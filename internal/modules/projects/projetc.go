package projects

import (
	"github.com/gin-gonic/gin"

	"github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/projects/service"
)

type IProjectController interface {
	CreateProject(c *gin.Context, in *domain.CreateProjectRequest) (*domain.ProjectResponse, error)
	GetProjectStatus(c *gin.Context, in *GetProjectStatusRequest) (*domain.ProjectStatusResponse, error)
	DeleteProject(c *gin.Context, in *GetProjectStatusRequest) (*domain.ProjectResponse, error)
	GetProjectMetrics(c *gin.Context, in *GetProjectStatusRequest) (*domain.NamespaceMetrics, error)
}

type ProjectHandler struct {
	ProjectService service.IProjectService
}

func NewProjectHandlers(ps service.IProjectService) *ProjectHandler {
	return &ProjectHandler{ProjectService: ps}
}

func (h *ProjectHandler) CreateProject(c *gin.Context, in *domain.CreateProjectRequest) (*domain.ProjectResponse, error) {
	userID, _ := c.Get("user_id")
	return h.ProjectService.CreateProject(c.Request.Context(), *in, userID.(string))
}

type GetProjectStatusRequest struct {
	ProjectID string `path:"id" desc:"ID du projet"`
}

func (h *ProjectHandler) GetProjectStatus(c *gin.Context, in *GetProjectStatusRequest) (*domain.ProjectStatusResponse, error) {
	return h.ProjectService.GetProjectStatus(c.Request.Context(), in.ProjectID)
}

func (h *ProjectHandler) DeleteProject(c *gin.Context, in *GetProjectStatusRequest) (*domain.ProjectResponse, error) {
	return h.ProjectService.DeleteProject(c.Request.Context(), in.ProjectID)
}

func (h *ProjectHandler) GetProjectMetrics(c *gin.Context, in *GetProjectStatusRequest) (*domain.NamespaceMetrics, error) {
	return h.ProjectService.GetMetrics(c.Request.Context(), in.ProjectID)
}

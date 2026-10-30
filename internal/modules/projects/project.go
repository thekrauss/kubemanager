package projects

import (
	"github.com/gin-gonic/gin"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/middleware/security"

	typeiam "github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/projects/service"
)

type IProjectController interface {
	CreateProject(c *gin.Context, in *typeiam.CreateProjectRequest) (*typeiam.ProjectResponse, error)
	GetProjectStatus(c *gin.Context, in *GetProjectStatusRequest) (*typeiam.ProjectStatusResponse, error)
	DeleteProject(c *gin.Context, in *GetProjectStatusRequest) (*typeiam.ProjectResponse, error)
	GetProjectMetrics(c *gin.Context, in *GetProjectStatusRequest) (*typeiam.NamespaceMetrics, error)
}

type ProjectHandler struct {
	ProjectService service.IProjectService
}

func NewProjectHandlers(ps service.IProjectService) *ProjectHandler {
	return &ProjectHandler{ProjectService: ps}
}

func (h *ProjectHandler) CreateProject(c *gin.Context, in *typeiam.CreateProjectRequest) (*typeiam.ProjectResponse, error) {
	// Fix #5 : clé "userID" (set par middleware/security) — avant on lisait
	// "user_id" qui renvoyait nil, puis .(string) panic type assertion.
	// Propriétaire : identifiant interne (celui des droits IAM), pas
	// l'identifiant Keycloak du jeton.
	owner, ok := security.InternalUserID(c)
	if !ok {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "complete your profile before creating a project")
	}
	return h.ProjectService.CreateProject(c.Request.Context(), *in, owner.String())
}

type GetProjectStatusRequest struct {
	ProjectID string `path:"id" desc:"ID du projet"`
}

func (h *ProjectHandler) GetProjectStatus(c *gin.Context, in *GetProjectStatusRequest) (*typeiam.ProjectStatusResponse, error) {
	return h.ProjectService.GetProjectStatus(c.Request.Context(), in.ProjectID)
}

func (h *ProjectHandler) DeleteProject(c *gin.Context, in *GetProjectStatusRequest) (*typeiam.ProjectResponse, error) {
	return h.ProjectService.DeleteProject(c.Request.Context(), in.ProjectID)
}

func (h *ProjectHandler) GetProjectMetrics(c *gin.Context, in *GetProjectStatusRequest) (*typeiam.NamespaceMetrics, error) {
	return h.ProjectService.GetMetrics(c.Request.Context(), in.ProjectID)
}

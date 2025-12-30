package projects

import (
	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/projects/service"
)

type ProjectHandler struct {
	ProjectService *service.ProjectService
}

func (h *ProjectHandler) Create(c *gin.Context, in *domain.CreateProjectRequest) (*domain.ProjectResponse, error) {
	// On récupère l'UserID injecté par ton AuthMiddleware
	userID, _ := c.Get("user_id")

	// On délègue au service
	return h.ProjectService.CreateProject(c.Request.Context(), *in, userID.(string))
}

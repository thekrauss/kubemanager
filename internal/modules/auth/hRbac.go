package controller

import (
	"github.com/gin-gonic/gin"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/middleware/security"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

type IRBACController interface {
	ListAllRoles(c *gin.Context) ([]domain.RoleDTO, error)
	AssignRole(c *gin.Context, in *domain.AssignRoleRequest) error
	ListProjectMembers(c *gin.Context, in *ListMembersInput) ([]domain.ProjectMemberDTO, error)
	RevokeProjectAccess(c *gin.Context, in *RevokeAccessInput) error
}

type RBACController struct {
	RBACService *service.RBACService
}

func NewRBACController(s *service.RBACService) *RBACController {
	return &RBACController{RBACService: s}
}

type ListMembersInput struct {
	ProjectID string `path:"projectID" validate:"required,uuid"`
}

type RevokeAccessInput struct {
	ProjectID string `path:"projectID" validate:"required,uuid"`
	UserID    string `path:"userID" validate:"required,uuid"`
}

func (ctrl *RBACController) ListAllRoles(c *gin.Context) ([]domain.RoleDTO, error) {
	return ctrl.RBACService.ListAllRoles(c.Request.Context())
}

func (ctrl *RBACController) AssignRole(c *gin.Context, in *domain.AssignRoleRequest) error {
	caller, ok := security.PrincipalFromContext(c)
	if !ok {
		return betoerrors.New(betoerrors.CodeUnauthorized, "authentication required")
	}
	return ctrl.RBACService.AssignProjectRole(c.Request.Context(), caller, in)
}

func (ctrl *RBACController) ListProjectMembers(c *gin.Context, in *ListMembersInput) ([]domain.ProjectMemberDTO, error) {
	return ctrl.RBACService.ListProjectMembers(c.Request.Context(), in.ProjectID)
}

func (ctrl *RBACController) RevokeProjectAccess(c *gin.Context, in *RevokeAccessInput) error {
	caller, ok := security.PrincipalFromContext(c)
	if !ok {
		return betoerrors.New(betoerrors.CodeUnauthorized, "authentication required")
	}
	return ctrl.RBACService.RevokeProjectAccess(c.Request.Context(), caller, in.ProjectID, in.UserID)
}

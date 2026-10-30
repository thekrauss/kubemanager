package controller

import (
	"github.com/gin-gonic/gin"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/middleware"
	"github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

type IAPIKeyController interface {
	ListKeys(c *gin.Context) ([]service.APIKeyDTO, error)
	GetKey(c *gin.Context, in *GetKeyInput) (*service.APIKeyDTO, error)
	ListAudit(c *gin.Context) ([]service.APIKeyAuditEntry, error)
	CreateKey(c *gin.Context, input *service.CreateAPIKeyInput) (*service.APIKeyCreatedResponse, error)
	RevokeKey(c *gin.Context, in *RevokeKeyInput) error
}

type APIKeyController struct {
	Service *service.APIKeyService
}

func NewAPIKeyController(s *service.APIKeyService) *APIKeyController {
	return &APIKeyController{Service: s}
}

func (ctrl *APIKeyController) ListKeys(c *gin.Context) ([]service.APIKeyDTO, error) {
	userID := c.GetString(middleware.ContextKeyUserID)
	if userID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	return ctrl.Service.ListUserKeys(c.Request.Context(), userID)
}

func (ctrl *APIKeyController) CreateKey(c *gin.Context, input *service.CreateAPIKeyInput) (*service.APIKeyCreatedResponse, error) {
	userID := c.GetString(middleware.ContextKeyUserID)
	if userID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	input.UserID = userID

	return ctrl.Service.CreateAPIKey(c.Request.Context(), *input)
}

type RevokeKeyInput struct {
	KeyID string `path:"id"`
}

func (ctrl *APIKeyController) RevokeKey(c *gin.Context, in *RevokeKeyInput) error {
	userID := c.GetString(middleware.ContextKeyUserID)
	return ctrl.Service.RevokeKey(c.Request.Context(), userID, in.KeyID)
}

type GetKeyInput struct {
	KeyID string `path:"id"`
}

// GetKey : introspection d'une clé (détails hors hash). Réservé au propriétaire de la clé.
func (ctrl *APIKeyController) GetKey(c *gin.Context, in *GetKeyInput) (*service.APIKeyDTO, error) {
	userID := c.GetString(middleware.ContextKeyUserID)
	if userID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	return ctrl.Service.GetKey(c.Request.Context(), userID, in.KeyID)
}

// ListAudit : vue "audit" de toutes les clés du user (usage_count, status, last_used_at).
func (ctrl *APIKeyController) ListAudit(c *gin.Context) ([]service.APIKeyAuditEntry, error) {
	userID := c.GetString(middleware.ContextKeyUserID)
	if userID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	return ctrl.Service.ListAudit(c.Request.Context(), userID)
}

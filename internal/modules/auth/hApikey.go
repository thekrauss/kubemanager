package controller

import (
	"github.com/gin-gonic/gin"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

type IAPIKeyController interface {
	ListKeys(c *gin.Context) ([]service.APIKeyDTO, error)
	GetKey(c *gin.Context, in *GetKeyInput) (*service.APIKeyDTO, error)
	ListAudit(c *gin.Context) ([]service.APIKeyAuditEntry, error)
	CreateKey(c *gin.Context, input *service.CreateAPIKeyInput) (*service.APIKeyCreatedResponse, error)
	CreateClusterKey(c *gin.Context, input *service.CreateClusterKeyInput) (*service.APIKeyCreatedResponse, error)
	ListClusterKeys(c *gin.Context) ([]service.ClusterKeyDTO, error)
	RotateClusterKey(c *gin.Context, in *RotateClusterKeyRequest) (*service.APIKeyCreatedResponse, error)
	RevokeKey(c *gin.Context, in *RevokeKeyInput) error
}

// RotateClusterKeyRequest : path param + body.
type RotateClusterKeyRequest struct {
	KeyID     string `path:"id" binding:"required"`
	GraceDays int    `json:"grace_days,omitempty"`
}

type APIKeyController struct {
	Service *service.APIKeyService
}

func NewAPIKeyController(s *service.APIKeyService) *APIKeyController {
	return &APIKeyController{Service: s}
}

func (ctrl *APIKeyController) ListKeys(c *gin.Context) ([]service.APIKeyDTO, error) {
	userID := c.GetString("userID")
	if userID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	return ctrl.Service.ListUserKeys(c.Request.Context(), userID)
}

func (ctrl *APIKeyController) CreateKey(c *gin.Context, input *service.CreateAPIKeyInput) (*service.APIKeyCreatedResponse, error) {
	userID := c.GetString("userID")
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
	userID := c.GetString("userID")
	return ctrl.Service.RevokeKey(c.Request.Context(), userID, in.KeyID)
}

type GetKeyInput struct {
	KeyID string `path:"id"`
}

// GetKey : introspection d'une clé (détails hors hash). Réservé au propriétaire de la clé.
func (ctrl *APIKeyController) GetKey(c *gin.Context, in *GetKeyInput) (*service.APIKeyDTO, error) {
	userID := c.GetString("userID")
	if userID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	return ctrl.Service.GetKey(c.Request.Context(), userID, in.KeyID)
}

// ListAudit : vue "audit" de toutes les clés du user (usage_count, status, last_used_at).
func (ctrl *APIKeyController) ListAudit(c *gin.Context) ([]service.APIKeyAuditEntry, error) {
	userID := c.GetString("userID")
	if userID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	return ctrl.Service.ListAudit(c.Request.Context(), userID)
}

// CreateClusterKey : admin-only. Provisionne une API key Kind=cluster pour le
// watcher K8s d'un cluster donné. L'RBAC (role admin) est appliqué côté route
// via RequireGlobalRole — ici on se contente de lire l'userID admin.
func (ctrl *APIKeyController) CreateClusterKey(c *gin.Context, input *service.CreateClusterKeyInput) (*service.APIKeyCreatedResponse, error) {
	adminID := c.GetString("userID")
	if adminID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	if input == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "body required")
	}
	input.CreatedBy = adminID
	return ctrl.Service.CreateClusterKey(c.Request.Context(), *input)
}

// ListClusterKeys : admin-only. Liste toutes les cluster keys avec leur statut.
func (ctrl *APIKeyController) ListClusterKeys(c *gin.Context) ([]service.ClusterKeyDTO, error) {
	return ctrl.Service.ListClusterKeys(c.Request.Context())
}

// RotateClusterKey : admin-only. Rotate une cluster key — génère une nouvelle
// clé pour le même cluster_id et programme l'expiration de l'ancienne à
// now+grace_days (défaut 7j).
func (ctrl *APIKeyController) RotateClusterKey(c *gin.Context, in *RotateClusterKeyRequest) (*service.APIKeyCreatedResponse, error) {
	adminID := c.GetString("userID")
	if adminID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "unauthorized")
	}
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "body required")
	}
	return ctrl.Service.RotateClusterKey(c.Request.Context(), service.RotateClusterKeyInput{
		KeyID:     in.KeyID,
		CreatedBy: adminID,
		GraceDays: in.GraceDays,
	})
}

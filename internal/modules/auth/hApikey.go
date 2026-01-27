package controller

import (
	"github.com/gin-gonic/gin"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

type IAPIKeyController interface {
	ListKeys(c *gin.Context) ([]service.APIKeyDTO, error)
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

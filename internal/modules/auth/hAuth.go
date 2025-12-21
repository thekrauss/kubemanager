package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

type AuthController struct {
	AuthService *service.AuthService
}

func NewAuthController(s *service.AuthService) *AuthController {
	return &AuthController{AuthService: s}
}

func (ctrl *AuthController) Login(c *gin.Context, in *domain.LoginRequest) (*domain.LoginResponse, error) {
	return ctrl.AuthService.Login(c.Request.Context(), in)
}

func (ctrl *AuthController) ValidateToken(c *gin.Context, in *domain.TokenRequest) (*domain.TokenResponse, error) {
	return ctrl.AuthService.ValidateToken(c.Request.Context(), in)
}

package controller

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

type AuthController struct {
	AuthService *service.AuthService
	RBACService *service.RBACService
}

func NewAuthController(authSvc *service.AuthService, rbacSvc *service.RBACService) *AuthController {
	return &AuthController{
		AuthService: authSvc,
		RBACService: rbacSvc,
	}
}

func (ctrl *AuthController) Login(c *gin.Context, in *domain.LoginRequest) (*domain.LoginResponse, error) {
	return ctrl.AuthService.Login(c.Request.Context(), in)
}

func (ctrl *AuthController) ValidateToken(c *gin.Context, in *domain.TokenRequest) (*domain.TokenResponse, error) {
	return ctrl.AuthService.ValidateToken(c.Request.Context(), in)
}

func (ctrl *AuthController) RefreshToken(c *gin.Context, in *domain.RefreshTokenRequest) (*domain.RefreshTokenResponse, error) {
	return ctrl.AuthService.RefreshToken(c.Request.Context(), in)
}

type ForgotPasswordInput struct {
	Email string `json:"email" binding:"required,email"`
}

func (ctrl *AuthController) Register(c *gin.Context, in *domain.RegisterRequest) (*domain.RegisterResponse, error) {
	user, err := ctrl.AuthService.CreateUser(c.Request.Context(), in)
	if err != nil {
		return nil, err
	}

	return &domain.RegisterResponse{
		ID:        user.ID.String(),
		Email:     user.Email,
		FullName:  user.FullName,
		CreatedAt: user.CreatedAt,
	}, nil
}

func (ctrl *AuthController) Logout(c *gin.Context, in *domain.LogoutRequest) error {
	userID := c.GetString("userID")
	if userID == "" {
		return errors.New(errors.CodeUnauthorized, "User ID not found in context")
	}

	authHeader := c.GetHeader("Authorization")
	accessToken := ""
	if len(authHeader) > 7 && strings.ToUpper(authHeader[0:7]) == "BEARER " {
		accessToken = authHeader[7:]
	}

	in.UserID = userID
	in.AccessToken = accessToken

	return ctrl.AuthService.Logout(c.Request.Context(), in)
}

type MessageResponse struct {
	Message    string `json:"message"`
	DebugToken string `json:"debug_token,omitempty"`
}

type ForgotPasswordEmailRequest struct {
	Email string `json:"email" binding:"required,email"`
}

func (ctrl *AuthController) ForgotPassword(c *gin.Context, in *ForgotPasswordEmailRequest) (*MessageResponse, error) {
	token, err := ctrl.AuthService.ForgotPassword(c.Request.Context(), in.Email)
	if err != nil {
		return nil, err
	}
	return &MessageResponse{Message: "Si cet email existe, un lien a été envoyé.", DebugToken: token}, nil
}

func (ctrl *AuthController) ResetPassword(c *gin.Context, in *service.ResetPasswordInput) (*MessageResponse, error) {
	if err := ctrl.AuthService.ResetPassword(c.Request.Context(), *in); err != nil {
		return nil, err
	}
	return &MessageResponse{Message: "Mot de passe réinitialisé avec succès."}, nil
}

func (ctrl *AuthController) ChangePassword(c *gin.Context, in *service.ChangePasswordInput) (*MessageResponse, error) {
	userID := c.GetString("userID")
	if userID == "" {
		return nil, errors.New(errors.CodeUnauthorized, "User ID not found in context")
	}
	in.UserID = userID

	if err := ctrl.AuthService.ChangePassword(c.Request.Context(), *in); err != nil {
		return nil, err
	}
	return &MessageResponse{Message: "Mot de passe modifié avec succès."}, nil
}

func (ctrl *AuthController) UpdateProfile(c *gin.Context, in *service.UpdateUserInput) (*domain.User, error) {
	updatedUser, err := ctrl.AuthService.UpdateUser(c.Request.Context(), *in)
	if err != nil {
		return nil, err
	}
	updatedUser.PasswordHash = ""
	return updatedUser, nil
}

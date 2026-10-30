package identity

import (
	"strings"
	"time"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/gin-gonic/gin"

	"github.com/thekrauss/kubemanager/internal/auth"
	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	identitysvc "github.com/thekrauss/kubemanager/internal/modules/identity/service"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

type Controller interface {
	// Register / bootstrap
	Register(c *gin.Context, in *identitytypes.RegisterRequest) (*identitytypes.RegisterResponse, error)
	Bootstrap(c *gin.Context, in *identitytypes.BootstrapRequest) (*identitytypes.BootstrapResponse, error)
	BootstrapStatus(c *gin.Context) (*identitytypes.BootstrapStatusResponse, error)

	// Auth
	Logout(c *gin.Context, in *identitytypes.LogoutRequest) (*identitytypes.LogoutResponse, error)
	RefreshToken(c *gin.Context, in *identitytypes.RefreshTokenRequest) (*identitytypes.RefreshTokenResponse, error)
	ValidateToken(c *gin.Context) (*identitytypes.TokenResponse, error)

	// Password
	ForgotPassword(c *gin.Context, in *identitytypes.ForgotPasswordRequest) error
	ChangePassword(c *gin.Context, in *identitytypes.ChangePasswordRequest) error

	// Login hébergé (PKCE) + social — callback commun SocialCallback
	LoginURL(c *gin.Context, in *identitytypes.LoginURLQuery) (*identitytypes.LoginURLResponse, error)
	SocialLoginURL(c *gin.Context, in *identitytypes.SocialLoginURLQuery) (*identitytypes.SocialLoginURLResponse, error)
	SocialCallback(c *gin.Context, in *identitytypes.SocialCallbackQuery) (*identitytypes.LoginResponse, error)

	// CLI : flux device (RFC 8628)
	DeviceCode(c *gin.Context)
	DeviceToken(c *gin.Context)

	// Verification
	ResendVerificationEmail(c *gin.Context) error
	ResendVerificationEmailForEmail(c *gin.Context, in *identitytypes.ForgotPasswordRequest) error
}

type controller struct {
	service   identitysvc.Service
	authCache cache.AuthCache // pour blacklist JTI au logout
}

func NewController(service identitysvc.Service, authCache cache.AuthCache) Controller {
	return &controller{service: service, authCache: authCache}
}

// ── Register / bootstrap ──────────────────────────────────────────────────

func (ctrl *controller) Register(c *gin.Context, in *identitytypes.RegisterRequest) (*identitytypes.RegisterResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	return ctrl.service.RegisterAccount(c.Request.Context(), *in, clientOf(c))
}

func (ctrl *controller) Bootstrap(c *gin.Context, in *identitytypes.BootstrapRequest) (*identitytypes.BootstrapResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	keycloakUserID := c.GetString("userID")
	if keycloakUserID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "authenticated user context missing")
	}
	email := c.GetString("email")
	return ctrl.service.Bootstrap(c.Request.Context(), identitysvc.BootstrapPrincipal{KeycloakUserID: keycloakUserID, Email: email}, *in)
}

func (ctrl *controller) BootstrapStatus(c *gin.Context) (*identitytypes.BootstrapStatusResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	keycloakUserID := c.GetString("userID")
	if keycloakUserID == "" {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "authenticated user context missing")
	}
	email := c.GetString("email")
	return ctrl.service.BootstrapStatus(c.Request.Context(), identitysvc.BootstrapPrincipal{KeycloakUserID: keycloakUserID, Email: email})
}

// ── Auth ──────────────────────────────────────────────────────────────────

func (ctrl *controller) Logout(c *gin.Context, in *identitytypes.LogoutRequest) (*identitytypes.LogoutResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	// Access token lu depuis le header Authorization (pas le body) pour pouvoir blacklister le JTI.
	accessToken := strings.TrimPrefix(strings.TrimSpace(c.GetHeader("Authorization")), "Bearer ")

	if strings.HasPrefix(in.RefreshToken, auth.RefreshTokenPrefix) {
		if err := ctrl.service.EndSession(c.Request.Context(), in.RefreshToken); err != nil {
			return nil, err
		}
		return &identitytypes.LogoutResponse{}, nil
	}
	// Session ouverte avant le passage aux sessions betorun (transition).
	if err := ctrl.service.Logout(c.Request.Context(), in.RefreshToken); err != nil {
		return nil, err
	}
	// Blacklist du JTI pour rejeter immédiatement ce token côté middleware auth.
	if ctrl.authCache != nil && accessToken != "" {
		if jti := identitysvc.ExtractJTI(accessToken); jti != "" {
			_ = ctrl.authCache.BlacklistAccessTokenJTI(c.Request.Context(), jti, 24*time.Hour)
		}
	}
	return &identitytypes.LogoutResponse{}, nil
}

func (ctrl *controller) RefreshToken(c *gin.Context, in *identitytypes.RefreshTokenRequest) (*identitytypes.RefreshTokenResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	if strings.HasPrefix(in.RefreshToken, auth.RefreshTokenPrefix) {
		return ctrl.service.RefreshSession(c.Request.Context(), in.RefreshToken)
	}
	// Session ouverte avant le passage aux sessions betorun (transition).
	tokens, err := ctrl.service.RefreshToken(c.Request.Context(), in.RefreshToken)
	if err != nil {
		return nil, err
	}
	return &identitytypes.RefreshTokenResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresIn:    tokens.ExpiresIn,
	}, nil
}

func (ctrl *controller) ValidateToken(c *gin.Context) (*identitytypes.TokenResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	// Le middleware auth a déjà validé le token — on remonte les claims du contexte.
	id, ok := security.InternalUserID(c)
	if !ok {
		return &identitytypes.TokenResponse{Valid: false}, nil
	}
	return &identitytypes.TokenResponse{Valid: true, UserID: id.String(), Email: c.GetString("email")}, nil
}

// ── Password ──────────────────────────────────────────────────────────────

func (ctrl *controller) ForgotPassword(c *gin.Context, in *identitytypes.ForgotPasswordRequest) error {
	if ctrl == nil || ctrl.service == nil {
		return identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	return ctrl.service.RequestPasswordReset(c.Request.Context(), in.Email, clientOf(c))
}

func (ctrl *controller) ChangePassword(c *gin.Context, in *identitytypes.ChangePasswordRequest) error {
	if ctrl == nil || ctrl.service == nil {
		return identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	keycloakUserID := c.GetString("userID")
	if keycloakUserID == "" {
		return betoerrors.New(betoerrors.CodeUnauthorized, "authenticated user context missing")
	}
	sid, _ := security.SessionID(c)
	return ctrl.service.ChangePassword(c.Request.Context(), keycloakUserID, sid, in.CurrentPassword, in.NewPassword)
}

// ── Login hébergé (Authorization Code + PKCE) ─────────────────────────────

func (ctrl *controller) LoginURL(c *gin.Context, in *identitytypes.LoginURLQuery) (*identitytypes.LoginURLResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	loginURL, err := ctrl.service.LoginURL(c.Request.Context(), in.RedirectURI,
		identitysvc.LoginOptions{StepUp: in.StepUp, CLI: in.Client == "cli"})
	if err != nil {
		return nil, err
	}
	return &identitytypes.LoginURLResponse{URL: loginURL}, nil
}

// ── Social login ──────────────────────────────────────────────────────────

func (ctrl *controller) SocialLoginURL(c *gin.Context, in *identitytypes.SocialLoginURLQuery) (*identitytypes.SocialLoginURLResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	loginURL, err := ctrl.service.SocialLoginURL(c.Request.Context(), in.Provider, in.RedirectURI)
	if err != nil {
		return nil, err
	}
	return &identitytypes.SocialLoginURLResponse{URL: loginURL, Provider: in.Provider}, nil
}

func (ctrl *controller) SocialCallback(c *gin.Context, in *identitytypes.SocialCallbackQuery) (*identitytypes.LoginResponse, error) {
	if ctrl == nil || ctrl.service == nil {
		return nil, identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	result, err := ctrl.service.HandleSocialCallback(c.Request.Context(), in.Code, in.State, in.RedirectURI)
	if err != nil {
		return nil, err
	}
	return &identitytypes.LoginResponse{
		AccessToken:    result.AccessToken,
		RefreshToken:   result.RefreshToken,
		ExpiresIn:      result.ExpiresIn,
		KeycloakUserID: result.KeycloakUserID,
		Email:          result.Email,
		Bootstrapped:   result.Bootstrapped,
	}, nil
}

// ── Verification ──────────────────────────────────────────────────────────

func (ctrl *controller) ResendVerificationEmail(c *gin.Context) error {
	if ctrl == nil || ctrl.service == nil {
		return identitysvc.ErrIdentityServiceUnavailable
	}
	email := c.GetString("email")
	if email == "" {
		return betoerrors.New(betoerrors.CodeUnauthorized, "authenticated user context missing")
	}
	return ctrl.service.ResendVerification(c.Request.Context(), email, clientOf(c))
}

func (ctrl *controller) ResendVerificationEmailForEmail(c *gin.Context, in *identitytypes.ForgotPasswordRequest) error {
	if ctrl == nil || ctrl.service == nil {
		return identitysvc.ErrIdentityServiceUnavailable
	}
	if in == nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "input is required")
	}
	return ctrl.service.ResendVerification(c.Request.Context(), in.Email, clientOf(c))
}

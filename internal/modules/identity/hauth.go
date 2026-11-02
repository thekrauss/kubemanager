package identity

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/auth"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	identitysvc "github.com/thekrauss/kubemanager/internal/modules/identity/service"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// Authentification gérée par betorun (docs/architecture/auth.md) : routes
// appelées par la console (Next.js) et la CLI.

// AuthController : connexion, sessions, second facteur, emails.
type AuthController struct {
	service identitysvc.Service
}

func NewAuthController(service identitysvc.Service) *AuthController {
	return &AuthController{service: service}
}

func clientOf(c *gin.Context) identitysvc.Client {
	return identitysvc.Client{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

// session : personne et session de la requête (jeton de session betorun).
func session(c *gin.Context) (uuid.UUID, uuid.UUID, error) {
	user, ok := security.InternalUserID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, betoerrors.New(betoerrors.CodeUnauthorized, "authenticated user context missing")
	}
	sid, ok := security.SessionID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, betoerrors.New(betoerrors.CodeUnauthorized, "sign in again to manage your security settings")
	}
	return user, sid, nil
}

func (a *AuthController) Login(c *gin.Context, in *identitytypes.PasswordLoginRequest) (*identitytypes.AuthResponse, error) {
	return a.service.PasswordLogin(c.Request.Context(), *in, clientOf(c))
}

func (a *AuthController) LoginMFA(c *gin.Context, in *identitytypes.MFALoginRequest) (*identitytypes.AuthResponse, error) {
	return a.service.MFALogin(c.Request.Context(), *in, clientOf(c))
}

func (a *AuthController) StepUp(c *gin.Context, in *identitytypes.StepUpRequest) (*identitytypes.AuthResponse, error) {
	user, sid, err := session(c)
	if err != nil {
		return nil, err
	}
	return a.service.StepUp(c.Request.Context(), user, sid, in.Code)
}

func (a *AuthController) ListSessions(c *gin.Context) ([]auth.Session, error) {
	user, sid, err := session(c)
	if err != nil {
		return nil, err
	}
	return a.service.ListSessions(c.Request.Context(), user, sid)
}

func (a *AuthController) RevokeSession(c *gin.Context, in *identitytypes.SessionPath) error {
	user, _, err := session(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return auth.ErrSessionNotFound
	}
	return a.service.RevokeSession(c.Request.Context(), user, id)
}

func (a *AuthController) RevokeOtherSessions(c *gin.Context) error {
	user, sid, err := session(c)
	if err != nil {
		return err
	}
	return a.service.RevokeOtherSessions(c.Request.Context(), user, sid)
}

func (a *AuthController) MFAStatus(c *gin.Context) (*auth.FactorStatus, error) {
	user, _, err := session(c)
	if err != nil {
		return nil, err
	}
	return a.service.MFAStatus(c.Request.Context(), user)
}

func (a *AuthController) StartTOTP(c *gin.Context) (*auth.TOTPEnrollment, error) {
	user, _, err := session(c)
	if err != nil {
		return nil, err
	}
	return a.service.StartTOTP(c.Request.Context(), user, c.GetString("email"))
}

func (a *AuthController) ConfirmTOTP(c *gin.Context, in *identitytypes.TOTPConfirmRequest) (*identitytypes.RecoveryCodesResponse, error) {
	user, sid, err := session(c)
	if err != nil {
		return nil, err
	}
	return a.service.ConfirmTOTP(c.Request.Context(), user, sid, in.Code)
}

func (a *AuthController) DisableTOTP(c *gin.Context) error {
	user, sid, err := session(c)
	if err != nil {
		return err
	}
	return a.service.DisableTOTP(c.Request.Context(), user, sid)
}

func (a *AuthController) RegenerateRecoveryCodes(c *gin.Context) (*identitytypes.RecoveryCodesResponse, error) {
	user, sid, err := session(c)
	if err != nil {
		return nil, err
	}
	return a.service.RegenerateRecoveryCodes(c.Request.Context(), user, sid)
}

func (a *AuthController) VerifyEmail(c *gin.Context, in *identitytypes.VerifyEmailRequest) error {
	return a.service.VerifyEmail(c.Request.Context(), in.Token)
}

func (a *AuthController) ResetPassword(c *gin.Context, in *identitytypes.ResetPasswordRequest) error {
	return a.service.CompletePasswordReset(c.Request.Context(), in.Token, in.Password)
}

// ── Connexion de la CLI (docs/architecture/auth.md §9) ──

func (a *AuthController) CLIAuthorizeURL(c *gin.Context, in *identitytypes.CLIAuthorizeQuery) (*identitytypes.URLResponse, error) {
	return a.service.CLIAuthorizeURL(c.Request.Context(), *in)
}

func (a *AuthController) AuthorizeCLI(c *gin.Context, in *identitytypes.CLIAuthorizeRequest) (*identitytypes.URLResponse, error) {
	user, sid, err := session(c)
	if err != nil {
		return nil, err
	}
	return a.service.AuthorizeCLI(c.Request.Context(), user, sid, *in)
}

func (a *AuthController) CLIToken(c *gin.Context, in *identitytypes.CLITokenRequest) (*identitytypes.AuthResponse, error) {
	return a.service.ExchangeCLICode(c.Request.Context(), *in, clientOf(c))
}

func (a *AuthController) DeviceInfo(c *gin.Context, in *identitytypes.DeviceCodeQuery) (*identitytypes.DeviceInfo, error) {
	if _, _, err := session(c); err != nil {
		return nil, err
	}
	return a.service.DeviceInfo(c.Request.Context(), in.UserCode)
}

func (a *AuthController) ApproveDevice(c *gin.Context, in *identitytypes.DeviceApproveRequest) error {
	user, sid, err := session(c)
	if err != nil {
		return err
	}
	return a.service.ApproveDevice(c.Request.Context(), user, sid, *in)
}

// ── Connexion Google / GitHub (docs/architecture/auth.md §8) ──

func (a *AuthController) SocialProviders(_ *gin.Context) (*identitytypes.SocialProvidersResponse, error) {
	return &identitytypes.SocialProvidersResponse{Providers: a.service.SocialProviders()}, nil
}

func (a *AuthController) SocialAuthorize(c *gin.Context, in *identitytypes.SocialProviderPath) (*identitytypes.URLResponse, error) {
	return a.service.SocialAuthorizeURL(c.Request.Context(), in.Provider)
}

func (a *AuthController) SocialCallback(c *gin.Context, in *identitytypes.SocialCallbackRequest) (*identitytypes.AuthResponse, error) {
	return a.service.SocialCallback(c.Request.Context(), in.Provider, *in, clientOf(c))
}

func (a *AuthController) ListIdentities(c *gin.Context) ([]auth.LinkedIdentity, error) {
	user, _, err := session(c)
	if err != nil {
		return nil, err
	}
	return a.service.ListIdentities(c.Request.Context(), user)
}

func (a *AuthController) UnlinkIdentity(c *gin.Context, in *identitytypes.SocialProviderPath) error {
	user, sid, err := session(c)
	if err != nil {
		return err
	}
	return a.service.UnlinkIdentity(c.Request.Context(), user, sid, c.GetString("userID"), in.Provider)
}

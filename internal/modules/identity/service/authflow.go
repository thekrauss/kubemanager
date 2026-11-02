package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/auth"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// Connexion gérée par betorun (docs/architecture/auth.md §5) : l'annuaire
// vérifie le mot de passe, betorun ouvre la session et émet ses jetons.

// Limites de tentatives (fenêtres fixes, Redis).
const (
	loginWindow      = 15 * time.Minute
	loginPerIP       = 30
	loginPerAccount  = 10
	mfaChallengeTTL  = 5 * time.Minute
	mfaAttempts      = 5
	stepUpWindow     = 15 * time.Minute
	stepUpPerSession = 10
	mailWindow       = time.Hour
	mailPerAccount   = 3
	mailPerIP        = 20
	registerPerIP    = 10
)

var (
	ErrInvalidCredentials  = errors.New("email or password is incorrect")
	ErrTooManyAttempts     = errors.New("too many attempts, try again later")
	ErrMFAChallengeExpired = errors.New("the sign-in has expired, start again")
	ErrAuthUnavailable     = errors.New("sign-in is temporarily unavailable")
	ErrWeakPassword        = errors.New("this password does not meet the security requirements")
	ErrCurrentPassword     = errors.New("your current password is incorrect")
)

// Limiter : compteur à fenêtre fixe (Redis, ADR 0006).
type Limiter interface {
	Increment(ctx context.Context, key string, window time.Duration) (int64, error)
}

// Client : contexte réseau d'une demande (journal des sessions).
type Client struct {
	IP        string
	UserAgent string
}

// mfaChallenge : connexion en attente du second facteur.
type mfaChallenge struct {
	UserID  uuid.UUID `json:"user_id"`
	Subject string    `json:"subject"`
	Kind    string    `json:"kind"`
	// AMR : méthode du premier facteur (mot de passe, google, github).
	AMR []string `json:"amr,omitempty"`
}

func emailKey(email string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(h[:12])
}

// limit : false si la limite est dépassée. Redis indisponible : refus (la
// connexion ne doit pas devenir illimitée silencieusement).
func (s *service) limit(ctx context.Context, key string, max int64, window time.Duration) error {
	if s.limiter == nil {
		return nil
	}
	n, err := s.limiter.Increment(ctx, "auth:limit:"+key, window)
	if err != nil {
		return ErrAuthUnavailable
	}
	if n > max {
		audit.Annotate(ctx, "rate_limited", key)
		return ErrTooManyAttempts
	}
	return nil
}

func (s *service) authReady() error {
	if s == nil || s.keycloak == nil || s.sessions == nil || s.factors == nil {
		return ErrAuthUnavailable
	}
	return nil
}

func sessionKind(client string) string {
	if client == auth.KindCLI {
		return auth.KindCLI
	}
	return auth.KindWeb
}

func authResponse(t *auth.Tokens) *identitytypes.AuthResponse {
	return &identitytypes.AuthResponse{Status: identitytypes.AuthStatusAuthenticated, AccessToken: t.AccessToken,
		ExpiresIn: int64(time.Until(t.ExpiresAt).Round(time.Second).Seconds()), ExpiresAt: t.ExpiresAt,
		RefreshToken: t.RefreshToken, SessionID: t.SessionID.String(), MFA: t.ACR == auth.ACRGold}
}

// PasswordLogin : vérifie le mot de passe, puis ouvre la session ou demande
// le second facteur.
func (s *service) PasswordLogin(ctx context.Context, in identitytypes.PasswordLoginRequest, cl Client) (*identitytypes.AuthResponse, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	email := normalizeEmail(in.Email)
	if err := s.limit(ctx, "login:ip:"+cl.IP, loginPerIP, loginWindow); err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "login:acct:"+emailKey(email), loginPerAccount, loginWindow); err != nil {
		return nil, err
	}
	check, err := s.keycloak.VerifyPassword(ctx, email, in.Password)
	switch {
	case errors.Is(err, keycloakadmin.ErrInvalidCredentials), errors.Is(err, keycloakadmin.ErrAccountDisabled):
		// Même réponse : un compte désactivé ne se distingue pas d'un mauvais mot de passe.
		audit.Annotate(ctx, "login", "failed")
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrAuthUnavailable, err)
	}
	if !check.EmailVerified {
		return nil, ErrEmailNotVerified
	}
	user, err := s.localUser(ctx, check.Subject, check.Email, check.Name)
	if err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "user", "user:"+user.ID.String())
	kind := sessionKind(in.Client)

	return s.completeLogin(ctx, user.ID, check.Subject, []string{auth.AMRPassword}, kind, cl)
}

// MFALogin : deuxième étape de la connexion.
func (s *service) MFALogin(ctx context.Context, in identitytypes.MFALoginRequest, cl Client) (*identitytypes.AuthResponse, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	key := "auth:mfa:" + strings.TrimSpace(in.Challenge)
	raw, err := s.stateStore.GetValue(ctx, key)
	if err != nil || raw == "" {
		return nil, ErrMFAChallengeExpired
	}
	var ch mfaChallenge
	if err := json.Unmarshal([]byte(raw), &ch); err != nil {
		return nil, ErrMFAChallengeExpired
	}
	if err := s.limit(ctx, "mfa:"+in.Challenge, mfaAttempts, mfaChallengeTTL); err != nil {
		_ = s.stateStore.DeleteValue(ctx, key)
		return nil, ErrMFAChallengeExpired
	}
	audit.Annotate(ctx, "user", "user:"+ch.UserID.String())
	method, err := s.factors.Verify(ctx, ch.UserID, in.Code)
	if err != nil {
		audit.Annotate(ctx, "mfa", "failed")
		return nil, err
	}
	_ = s.stateStore.DeleteValue(ctx, key)
	amr := ch.AMR
	if len(amr) == 0 {
		amr = []string{auth.AMRPassword}
	}
	tokens, err := s.sessions.Create(ctx, auth.NewSession{UserID: ch.UserID, Kind: ch.Kind,
		AMR: append(amr, auth.AMROTP), MFA: true, IP: cl.IP, UserAgent: cl.UserAgent})
	if err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "login", "succeeded")
	audit.Annotate(ctx, "mfa_method", method)
	if user, err := s.users.FindByKeycloakUserID(ctx, ch.Subject); err == nil && user != nil {
		s.touchLastLogin(ctx, user)
	}
	return authResponse(tokens), nil
}

// RefreshSession : rotation du jeton de rafraîchissement.
func (s *service) RefreshSession(ctx context.Context, refreshToken string) (*identitytypes.RefreshTokenResponse, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	t, err := s.sessions.Refresh(ctx, refreshToken)
	if err != nil {
		if errors.Is(err, auth.ErrRefreshReused) {
			audit.Annotate(ctx, "session", "refresh_reuse_revoked")
		}
		return nil, err
	}
	return &identitytypes.RefreshTokenResponse{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken,
		ExpiresIn: int64(time.Until(t.ExpiresAt).Round(time.Second).Seconds())}, nil
}

// EndSession : déconnexion.
func (s *service) EndSession(ctx context.Context, refreshToken string) error {
	if err := s.authReady(); err != nil {
		return err
	}
	return s.sessions.RevokeByRefreshToken(ctx, refreshToken)
}

// StepUp : second facteur vérifié dans la session courante (action sensible).
func (s *service) StepUp(ctx context.Context, userID, sessionID uuid.UUID, code string) (*identitytypes.AuthResponse, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "stepup:"+sessionID.String(), stepUpPerSession, stepUpWindow); err != nil {
		return nil, err
	}
	method, err := s.factors.Verify(ctx, userID, code)
	if err != nil {
		audit.Annotate(ctx, "mfa", "failed")
		return nil, err
	}
	audit.Annotate(ctx, "mfa_method", method)
	t, err := s.sessions.MarkMFA(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	return authResponse(t), nil
}

func (s *service) ListSessions(ctx context.Context, userID, current uuid.UUID) ([]auth.Session, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	return s.sessions.List(ctx, userID, current)
}

func (s *service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	if err := s.authReady(); err != nil {
		return err
	}
	return s.sessions.Revoke(ctx, userID, sessionID, "user")
}

func (s *service) RevokeOtherSessions(ctx context.Context, userID, current uuid.UUID) error {
	if err := s.authReady(); err != nil {
		return err
	}
	return s.sessions.RevokeAll(ctx, userID, current, "signout_others")
}

// localUser : profil local du compte (créé à la première connexion).
func (s *service) localUser(ctx context.Context, subject, email, name string) (*domain.User, error) {
	user, err := s.users.FindByKeycloakUserID(ctx, subject)
	if err == nil && user != nil {
		return user, nil
	}
	return s.syncLocalUser(ctx, subject, email, name, "")
}

func randomID() (string, error) {
	return auth.RandomToken()
}

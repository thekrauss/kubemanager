// login.go — flux d'authentification user-facing :
//   - Login / Logout / RefreshToken (délégués à Keycloak)
//   - ForgotPassword / ResetPassword (required-action email + admin reset)
//   - LoginURL / SocialLoginURL / HandleSocialCallback (Authorization Code +
//     PKCE S256 + state CSRF)
//   - ResendVerificationEmail
//
// Adapté du pattern gophercart (identity/services/service.go).
package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
)

// pgUniqueViolation : code SQLSTATE Postgres pour unique_violation.
const pgUniqueViolation = "23505"

// ── Login ────────────────────────────────────────────────────────────────

func (s *service) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	if s == nil || s.keycloak == nil {
		return nil, ErrIdentityServiceUnavailable
	}
	email = normalizeEmail(email)
	if email == "" || strings.TrimSpace(password) == "" {
		return nil, ErrRegisterInputInvalid
	}

	tokens, err := s.keycloak.Login(ctx, email, password)
	if err != nil {
		return nil, err
	}

	claims := extractJWTClaims(tokens.AccessToken)
	if !claims.EmailVerified {
		return nil, ErrEmailNotVerified
	}

	sub := claims.Sub
	bootstrapped := false
	if sub != "" {
		user, dbErr := s.users.FindByKeycloakUserID(ctx, sub)
		switch {
		case dbErr == nil && user != nil:
			// Le user local existe — on est bootstrappé s'il a au moins un project membership.
			member, mErr := s.projectMembers.FindFirstActiveByUser(ctx, user.ID)
			if mErr == nil && member != nil {
				bootstrapped = true
			}
			s.touchLastLogin(ctx, user)
		case errors.Is(dbErr, gorm.ErrRecordNotFound):
			// Auto-provision silent du user local au premier login (sans projet).
			_, _ = s.syncLocalUser(ctx, sub, email, claims.Name, "")
		}
	}

	return &LoginResult{
		AccessToken:    tokens.AccessToken,
		RefreshToken:   tokens.RefreshToken,
		ExpiresIn:      tokens.ExpiresIn,
		KeycloakUserID: sub,
		Email:          email,
		Bootstrapped:   bootstrapped,
	}, nil
}

// ── Logout / Refresh ─────────────────────────────────────────────────────

func (s *service) Logout(ctx context.Context, refreshToken string) error {
	if s == nil || s.keycloak == nil {
		return ErrIdentityServiceUnavailable
	}
	return s.keycloak.Logout(ctx, refreshToken)
}

func (s *service) RefreshToken(ctx context.Context, refreshToken string) (*keycloakadmin.TokenPair, error) {
	if s == nil || s.keycloak == nil {
		return nil, ErrIdentityServiceUnavailable
	}
	return s.keycloak.RefreshToken(ctx, refreshToken)
}

// ── Password reset (Keycloak-driven) ─────────────────────────────────────

func (s *service) ForgotPassword(ctx context.Context, email string) error {
	if s == nil || s.keycloak == nil {
		return ErrIdentityServiceUnavailable
	}
	email = normalizeEmail(email)
	if email == "" {
		return ErrRegisterInputInvalid
	}
	kcUser, err := s.keycloak.FindUserByEmail(ctx, email)
	if err != nil {
		// Silent success si user inconnu — pour ne pas divulguer l'existence du compte.
		if errors.Is(err, keycloakadmin.ErrUserNotFound) {
			return nil
		}
		return err
	}
	return s.keycloak.ExecuteActionsEmail(ctx, kcUser.ID,
		[]string{"UPDATE_PASSWORD"}, s.actionEmailOptions())
}

func (s *service) ResetPassword(ctx context.Context, keycloakUserID, newPassword string) error {
	if s == nil || s.keycloak == nil {
		return ErrIdentityServiceUnavailable
	}
	return s.keycloak.ResetPassword(ctx, keycloakUserID, newPassword)
}

// ── Social login (Google / GitHub via Keycloak IDP broker) ──────────────

// hostedLoginProvider : marqueur du login via la page Keycloak (sans IdP social).
const hostedLoginProvider = "keycloak"

// oauthState : mémorisé côté serveur entre l'URL d'autorisation et le callback.
type oauthState struct {
	Provider     string `json:"p"`
	CodeVerifier string `json:"v"`
}

// LoginURL construit l'URL de la page de login Keycloak (Authorization Code +
// PKCE). Le callback est le même que pour le social : HandleSocialCallback.
func (s *service) LoginURL(ctx context.Context, redirectURI string) (string, error) {
	if s == nil || s.keycloak == nil {
		return "", ErrIdentityServiceUnavailable
	}
	return s.authorizationURL(ctx, hostedLoginProvider, "", redirectURI)
}

// SocialLoginURL construit l'URL d'autorisation Keycloak + kc_idp_hint.
func (s *service) SocialLoginURL(ctx context.Context, provider, redirectURI string) (string, error) {
	if s == nil || s.keycloak == nil {
		return "", ErrIdentityServiceUnavailable
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	idpAlias, ok := validProviders[provider]
	if !ok {
		return "", ErrUnknownProvider
	}
	return s.authorizationURL(ctx, provider, idpAlias, redirectURI)
}

// authorizationURL : un state CSRF unique + le code_verifier PKCE sont stockés
// dans StateStore (Redis en prod) avec TTL 10min, one-time use côté callback.
// Fix H9 : store distribué pour que pod B puisse valider un state créé par pod A.
// Le verifier ne quitte jamais le serveur ; seul son challenge S256 part dans l'URL.
func (s *service) authorizationURL(ctx context.Context, provider, idpAlias, redirectURI string) (string, error) {
	if s.stateStore == nil {
		return "", ErrIdentityServiceUnavailable
	}

	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		redirectURI = s.socialRedirectURI
	}
	if redirectURI == "" {
		return "", betoerrors.New(betoerrors.CodeInvalidInput, "redirect_uri is required")
	}

	stateID, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	state := stateID.String()
	verifier, challenge, err := newPKCEPair()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(oauthState{Provider: provider, CodeVerifier: verifier})
	if err != nil {
		return "", err
	}
	if err := s.stateStore.SetValue(ctx, "oauth:state:"+state, string(payload), 10*time.Minute); err != nil {
		return "", err
	}

	return s.keycloak.GetAuthorizationURL(idpAlias, redirectURI, state, challenge), nil
}

// newPKCEPair : code_verifier aléatoire (32 octets → 43 car. base64url, RFC 7636)
// et son code_challenge S256.
func newPKCEPair() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// HandleSocialCallback : valide le state, échange le code (+ verifier PKCE) contre
// un token, provisionne le user local. Sert au login hébergé comme au social.
func (s *service) HandleSocialCallback(ctx context.Context, code, state, redirectURI string) (*LoginResult, error) {
	if s == nil || s.keycloak == nil {
		return nil, ErrIdentityServiceUnavailable
	}
	if s.stateStore == nil {
		return nil, ErrIdentityServiceUnavailable
	}

	// Validation state CSRF + consommation (one-time) via store distribué.
	// Fix H9 : lu sur N'IMPORTE quel pod puisque stocké en Redis.
	stateKey := "oauth:state:" + strings.TrimSpace(state)
	stored, err := s.stateStore.GetValue(ctx, stateKey)
	if err != nil || stored == "" {
		return nil, ErrInvalidState
	}
	_ = s.stateStore.DeleteValue(ctx, stateKey)
	var st oauthState
	if err := json.Unmarshal([]byte(stored), &st); err != nil || st.CodeVerifier == "" {
		return nil, ErrInvalidState
	}

	tokens, err := s.keycloak.ExchangeCodeForToken(ctx, code, redirectURI, st.CodeVerifier)
	if err != nil {
		return nil, err
	}

	claims := extractJWTClaims(tokens.AccessToken)
	if !claims.EmailVerified {
		return nil, ErrEmailNotVerified
	}
	sub := claims.Sub
	if sub == "" {
		return nil, ErrSubjectInvalid
	}

	kcUser, err := s.keycloak.GetUserByID(ctx, sub)
	if err != nil {
		return nil, err
	}
	email := normalizeEmail(kcUser.Email)
	fullName := firstNonEmpty(kcUser.FullName, claims.Name, deriveDisplayName(email))
	avatarURL := strings.TrimSpace(kcUser.AvatarURL)

	user, dbErr := s.users.FindByKeycloakUserID(ctx, sub)
	if dbErr != nil && !errors.Is(dbErr, gorm.ErrRecordNotFound) {
		return nil, dbErr
	}

	bootstrapped := false
	if user == nil {
		// Premier login (hébergé ou social) → provision user local (pas de projet par défaut).
		user, err = s.syncLocalUser(ctx, sub, email, fullName, avatarURL)
		if err != nil {
			return nil, err
		}
	} else {
		// Sync minimal : avatar + last_login.
		mutated := false
		if avatarURL != "" && user.AvatarURL != avatarURL {
			user.AvatarURL = avatarURL
			mutated = true
		}
		if mutated {
			_ = s.users.UpdateUser(ctx, user)
		}
		s.touchLastLogin(ctx, user)
	}
	// bootstrapped = true dès qu'il y a un membership actif
	if member, mErr := s.projectMembers.FindFirstActiveByUser(ctx, user.ID); mErr == nil && member != nil {
		bootstrapped = true
	}

	return &LoginResult{
		AccessToken:    tokens.AccessToken,
		RefreshToken:   tokens.RefreshToken,
		ExpiresIn:      tokens.ExpiresIn,
		KeycloakUserID: sub,
		Email:          email,
		AvatarURL:      avatarURL,
		Bootstrapped:   bootstrapped,
	}, nil
}

// ── Verification emails ──────────────────────────────────────────────────

func (s *service) ResendVerificationEmail(ctx context.Context, keycloakUserID string) error {
	if s == nil || s.keycloak == nil {
		return ErrIdentityServiceUnavailable
	}
	keycloakUserID = strings.TrimSpace(keycloakUserID)
	if keycloakUserID == "" {
		return ErrRegisterInputInvalid
	}
	return s.keycloak.SendVerificationEmail(ctx, keycloakUserID, s.actionEmailOptions())
}

func (s *service) ResendVerificationEmailByEmail(ctx context.Context, email string) error {
	if s == nil || s.keycloak == nil {
		return ErrIdentityServiceUnavailable
	}
	email = normalizeEmail(email)
	if email == "" {
		return ErrRegisterInputInvalid
	}
	kcUser, err := s.keycloak.FindUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, keycloakadmin.ErrUserNotFound) {
			return nil
		}
		return err
	}
	return s.keycloak.SendVerificationEmail(ctx, kcUser.ID, s.actionEmailOptions())
}

func (s *service) actionEmailOptions() *keycloakadmin.ActionEmailOptions {
	if s.actionEmailClientID == "" && s.frontendBaseURL == "" {
		return nil
	}
	return &keycloakadmin.ActionEmailOptions{
		ClientID:    s.actionEmailClientID,
		RedirectURI: s.frontendBaseURL,
	}
}

// ── User sync helpers ────────────────────────────────────────────────────

// syncLocalUser crée un User local à partir du sub Keycloak.
// L'ID local = sub si c'est un UUID, sinon UUID généré.
//
// Fix H6 : si deux logins concurrents tentent de créer le même sub,
// l'INSERT perdant échoue avec unique_violation. On re-fetch pour retourner
// le user créé par le concurrent gagnant — l'appelant voit un résultat
// cohérent au lieu d'une 500.
func (s *service) syncLocalUser(ctx context.Context, keycloakUserID, email, fullName, avatarURL string) (*domain.User, error) {
	now := time.Now().UTC()
	var localID uuid.UUID
	if parsed, err := uuid.Parse(keycloakUserID); err == nil {
		localID = parsed
	} else {
		localID = uuid.New()
	}
	user := &domain.User{
		ID:             localID,
		KeycloakUserID: keycloakUserID,
		Email:          email,
		FullName:       firstNonEmpty(fullName, deriveDisplayName(email)),
		AvatarURL:      avatarURL,
		Role:           domain.RoleTypes.User,
		Status:         "active",
		IsActive:       true,
		IsVerified:     true,
		LastLoginAt:    &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.users.CreateUser(ctx, user); err != nil {
		// Race H6 : un login concurrent du même sub a gagné la course.
		// On re-fetch pour retourner le user existant au lieu de 500.
		if isUniqueViolation(err) {
			if existing, lookupErr := s.users.FindByKeycloakUserID(ctx, keycloakUserID); lookupErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, err
	}
	return user, nil
}

// isUniqueViolation détecte un conflit d'unicité Postgres (SQLSTATE 23505).
// Robuste à l'enchaînement d'erreurs wrappées par GORM.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgUniqueViolation
	}
	// Fallback : match sur le message pour couvrir les drivers qui n'exposent
	// pas PgError directement (ex: go-sql-driver wrappers).
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "unique violation")
}

func (s *service) touchLastLogin(ctx context.Context, user *domain.User) {
	now := time.Now().UTC()
	user.LastLoginAt = &now
	_ = s.users.UpdateUser(ctx, user)
}

// ── JWT claim decoding (no signature check — use only on fresh Keycloak tokens) ──

type jwtClaims struct {
	Sub           string `json:"sub"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

func extractJWTClaims(tokenString string) jwtClaims {
	parts := strings.SplitN(tokenString, ".", 3)
	if len(parts) != 3 {
		return jwtClaims{}
	}
	seg := parts[1]
	if rem := len(seg) % 4; rem != 0 {
		seg += strings.Repeat("=", 4-rem)
	}
	decoded, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return jwtClaims{}
	}
	var claims jwtClaims
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return jwtClaims{}
	}
	claims.Sub = strings.TrimSpace(claims.Sub)
	return claims
}

// ExtractJTI decodes the JTI claim from a JWT access token (without signature
// verification). Exposed so the controller can blacklist a token on logout.
func ExtractJTI(tokenString string) string {
	tokenString = strings.TrimPrefix(strings.TrimSpace(tokenString), "Bearer ")
	parts := strings.SplitN(tokenString, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	seg := parts[1]
	if rem := len(seg) % 4; rem != 0 {
		seg += strings.Repeat("=", 4-rem)
	}
	decoded, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return ""
	}
	var payload struct {
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.JTI)
}

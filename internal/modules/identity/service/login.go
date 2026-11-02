// login.go — profil local et sessions de l'ancien fournisseur :
//   - Logout / RefreshToken : sessions Keycloak ouvertes avant le passage
//     aux sessions betorun (transition, retirées en phase 3) ;
//   - synchronisation du profil local (première connexion).
//
// L'authentification elle-même : authflow.go, mfa.go, emailflow.go, cliauth.go.
package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
)

// pgUniqueViolation : code SQLSTATE Postgres pour unique_violation.
const pgUniqueViolation = "23505"

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

// cliRedirectRe : seule adresse de retour acceptée pour la CLI (boucle locale, RFC 8252).
var cliRedirectRe = regexp.MustCompile(`^http://127\.0\.0\.1:[0-9]{1,5}/callback$`)

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

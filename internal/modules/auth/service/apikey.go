// apikey.go — service de gestion des API keys.
//
// Pattern (calqué sur gophercart) :
//   - Format clé  : "km_<env>_<12 hex>_<40 hex>"  → prefix "km_<env>_<12 hex>"
//   - Stockage    : prefix (non secret, indexé) + sha256(full_key)
//   - Rotation    : rate-limited via Options (WithRotationPolicy)
//   - Cache       : validation TTL 30s + revocation prefix TTL 30j (AuthCache)
//   - ValidateKey : extraction prefix → lookup DB → compare hash → inject scopes
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	authcache "github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
)

const (
	apiKeyDefaultEnvTag     = "prod"
	apiKeyPrefixRandomBytes = 6  // 12 hex chars
	apiKeySecretBytes       = 20 // 40 hex chars
	apiKeyValidationTTL     = 30 * time.Second
	apiKeyRevocationTTL     = 30 * 24 * time.Hour
)

// ── Options pattern (Deps pattern gophercart) ────────────────────────────

type APIKeyServiceOption func(*APIKeyService)

// WithCache branche un AuthCache pour accélérer la validation + propager la révocation.
func WithCache(c authcache.AuthCache) APIKeyServiceOption {
	return func(s *APIKeyService) { s.cache = c }
}

// WithEnvTag insère un tag d'environnement dans le prefix ("km_prod_...", "km_dev_...").
func WithEnvTag(env string) APIKeyServiceOption {
	return func(s *APIKeyService) {
		env = strings.ToLower(strings.TrimSpace(env))
		if env != "" {
			s.envTag = env
		}
	}
}

// ── Service ──────────────────────────────────────────────────────────────

type APIKeyService struct {
	Repo  repository.AuthRepository
	cache authcache.AuthCache

	envTag string
}

func NewAPIKeyService(repo repository.AuthRepository, opts ...APIKeyServiceOption) *APIKeyService {
	s := &APIKeyService{
		Repo:   repo,
		envTag: apiKeyDefaultEnvTag,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateClusterKeyInput : demande admin pour provisionner une API key attribuée
// à un cluster K8s (watcher). Contrairement aux user keys, pas de scopes libres
// — les permissions sont codifiées par le Kind "cluster" côté middleware.
type CreateClusterKeyInput struct {
	// CreatedBy : userID de l'admin qui provisionne la clé. Stocké comme
	// "owner" dans la DB pour traçabilité audit (qui a créé cette cluster key).
	CreatedBy string     `json:"-"`
	Name      string     `json:"name"       binding:"required,min=2,max=120" desc:"Nom descriptif (ex: 'watcher-prod-fr-north-1')"`
	ClusterID string     `json:"cluster_id" binding:"required"               desc:"Identifiant du cluster cible (ex: prod-fr-north-1)"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"                        desc:"Expiration optionnelle (recommandé pour rotation)"`
}

// CreateClusterKey : provisionne une API key Kind=cluster pour le watcher.
//
// Différences vs CreateAPIKey user :
//   - Kind forcé à "cluster" (gate par middleware RequireAPIKeyKind)
//   - ProjectID = uuid.Nil (cross-project, pas lié à un projet spécifique)
//   - Scopes : single scope "cluster:report" (le watcher ne fait QUE pousser des events)
//   - Rate-limit rotation : non appliqué (les cluster keys sont créées à la main par admin)
//   - ClusterID stocké dans le Name pour retrouver la clé d'un cluster donné
func (s *APIKeyService) CreateClusterKey(ctx context.Context, input CreateClusterKeyInput) (*APIKeyCreatedResponse, error) {
	adminID, err := uuid.Parse(input.CreatedBy)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid admin user id")
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.ClusterID) == "" {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "name and cluster_id are required")
	}

	prefix, fullKey, err := s.generateKey()
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInternal, "failed to generate key")
	}

	apiKey := &domain.APIKey{
		UserID:    adminID,  // admin qui a créé la clé (audit trail)
		ProjectID: uuid.Nil, // cross-project, pas de rattachement
		ClusterID: strings.TrimSpace(input.ClusterID),
		Kind:      domain.APIKeyKindCluster,
		Name:      strings.TrimSpace(input.Name),
		Prefix:    prefix,
		KeyHash:   hashKey(fullKey),
		Scopes:    []string{"cluster:report"},
		ExpiresAt: input.ExpiresAt,
		IsActive:  true,
	}

	if err := s.Repo.CreateAPIKey(ctx, apiKey); err != nil {
		return nil, betoerrors.Wrap(err, betoerrors.CodeInternal, "failed to save cluster api key")
	}

	return &APIKeyCreatedResponse{
		ID:        apiKey.ID.String(),
		Name:      apiKey.Name,
		Prefix:    prefix,
		RawAPIKey: fullKey,
		Scopes:    []string(apiKey.Scopes),
		CreatedAt: apiKey.CreatedAt,
		ExpiresAt: apiKey.ExpiresAt,
	}, nil
}

// ClusterKeyDTO : vue admin d'une cluster key (sans hash ni secret).
type ClusterKeyDTO struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ClusterID  string     `json:"cluster_id"`
	Prefix     string     `json:"prefix"`
	Status     string     `json:"status"` // "active" | "expiring" | "revoked" | "expired"
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	UsageCount int        `json:"usage_count"`
}

// RotateClusterKeyInput : demande de rotation.
type RotateClusterKeyInput struct {
	KeyID     string `json:"-"`
	CreatedBy string `json:"-"`
	// GraceDays : nombre de jours avant que l'ancienne clé soit expirée.
	// 0 → défaut 7 jours (laisse le temps au watcher de redéployer avec la new key).
	// Négatif → expire immédiatement.
	GraceDays int `json:"grace_days,omitempty" desc:"Période de transition avant invalidation de l'ancienne clé (défaut 7j)"`
}

// RotateClusterKey : génère une nouvelle cluster key pour le même cluster_id,
// et programme l'expiration de l'ancienne à now + grace_days.
//
// Sécurité : l'ancienne clé continue de fonctionner pendant la grace period —
// le watcher peut être redéployé avec la new key, check qu'il reconnecte OK,
// puis révoquer manuellement. Si la window expire sans intervention, l'ancienne
// clé devient automatiquement invalide (middleware check `ExpiresAt`).
//
// Retourne la new raw key (affichée 1× seulement, comme Create).
func (s *APIKeyService) RotateClusterKey(ctx context.Context, input RotateClusterKeyInput) (*APIKeyCreatedResponse, error) {
	oldKeyID, err := uuid.Parse(input.KeyID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid key id")
	}
	adminID, err := uuid.Parse(input.CreatedBy)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid admin user id")
	}

	oldKey, err := s.Repo.GetAPIKeyByID(ctx, oldKeyID)
	if err != nil || oldKey == nil {
		return nil, betoerrors.New(betoerrors.CodeDBNotFound, "cluster key not found")
	}
	if oldKey.Kind != domain.APIKeyKindCluster {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "key is not a cluster key")
	}
	if oldKey.ClusterID == "" {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "cluster key missing cluster_id (legacy data?)")
	}

	// 1. Créer la nouvelle clé (même cluster, même name + suffix rotation).
	newResp, err := s.CreateClusterKey(ctx, CreateClusterKeyInput{
		CreatedBy: adminID.String(),
		Name:      fmt.Sprintf("%s (rotated %s)", oldKey.Name, time.Now().UTC().Format("2006-01-02")),
		ClusterID: oldKey.ClusterID,
	})
	if err != nil {
		return nil, fmt.Errorf("create new cluster key: %w", err)
	}

	// 2. Programmer l'expiration de l'ancienne.
	graceDays := input.GraceDays
	if graceDays == 0 {
		graceDays = 7
	}
	expireAt := time.Now().Add(time.Duration(graceDays) * 24 * time.Hour)
	if graceDays < 0 {
		expireAt = time.Now().Add(-time.Second) // immédiat
	}
	if err := s.Repo.UpdateAPIKeyExpiration(ctx, oldKeyID, &expireAt); err != nil {
		return nil, fmt.Errorf("set expiration on old key: %w", err)
	}

	// 3. Invalidate cache pour que la date d'expiration se propage vite.
	if s.cache != nil {
		_ = s.cache.RevokeAPIKeyPrefix(ctx, oldKey.Prefix, apiKeyRevocationTTL)
	}

	return newResp, nil
}

// ListClusterKeys : admin view de toutes les cluster keys, tous statuts confondus.
// Le status est calculé côté service pour simplifier le frontend.
func (s *APIKeyService) ListClusterKeys(ctx context.Context) ([]ClusterKeyDTO, error) {
	keys, err := s.Repo.ListAPIKeysByKind(ctx, domain.APIKeyKindCluster)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]ClusterKeyDTO, 0, len(keys))
	for _, k := range keys {
		status := "active"
		switch {
		case k.RevokedAt != nil || !k.IsActive:
			status = "revoked"
		case k.ExpiresAt != nil && now.After(*k.ExpiresAt):
			status = "expired"
		case k.ExpiresAt != nil && k.ExpiresAt.Sub(now) < 7*24*time.Hour:
			// "expiring" = < 7 jours avant expiration → l'admin doit rotate.
			status = "expiring"
		}
		out = append(out, ClusterKeyDTO{
			ID:         k.ID.String(),
			Name:       k.Name,
			ClusterID:  k.ClusterID,
			Prefix:     k.Prefix,
			Status:     status,
			CreatedAt:  k.CreatedAt,
			LastUsedAt: k.LastUsedAt,
			ExpiresAt:  k.ExpiresAt,
			RevokedAt:  k.RevokedAt,
			UsageCount: k.UsageCount,
		})
	}
	return out, nil
}

type CreateAPIKeyInput struct {
	UserID    string     `json:"-"`
	ProjectID string     `json:"project_id,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	Name      string     `json:"name"   validate:"required,min=2,max=120"`
	Scopes    []string   `json:"scopes" validate:"required,min=1"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type APIKeyCreatedResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`  // partie publique "km_prod_ab12cd34ef56"
	RawAPIKey string     `json:"api_key"` // TOUT : prefix + secret — affiché UNE fois
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// ErrUserAPIKeysRetired : les clés API rattachées à un utilisateur ne sont
// plus émises (docs/architecture/iam.md §8). Les clés existantes restent
// listables et révocables pendant leur période de transition.
var ErrUserAPIKeysRetired = errors.New("user API keys are no longer issued: create a service account key with " +
	"POST /kmanager/v1/projects/{projectID}/serviceAccounts/{serviceAccountID}/apiKeys")

// CreateAPIKey : retiré, voir ErrUserAPIKeysRetired.
func (s *APIKeyService) CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (*APIKeyCreatedResponse, error) {
	return nil, ErrUserAPIKeysRetired
}

type APIKeyDTO struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Kind       string     `json:"kind,omitempty"`
	Scopes     []string   `json:"scopes,omitempty"`
	UsageCount int        `json:"usage_count"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// APIKeyAuditEntry : entrée "audit" dérivée des métadonnées de la clé.
// NB : pas d'historique détaillé (il faudrait une table dédiée) — ce sont les
// données qu'on peut extraire de `APIKey` sans schema change.
type APIKeyAuditEntry struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Kind       string     `json:"kind,omitempty"`
	UsageCount int        `json:"usage_count"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Status     string     `json:"status"` // "active" | "revoked" | "expired"
}

func (s *APIKeyService) ListUserKeys(ctx context.Context, userIDStr string) ([]APIKeyDTO, error) {
	uID, _ := uuid.Parse(userIDStr)
	keys, err := s.Repo.ListUserAPIKeys(ctx, uID)
	if err != nil {
		return nil, err
	}
	result := make([]APIKeyDTO, 0, len(keys))
	for _, k := range keys {
		result = append(result, toAPIKeyDTO(k))
	}
	return result, nil
}

// GetKey retourne les détails d'une clé unique pour introspection.
// Vérifie que la clé appartient bien à l'utilisateur (pas de leak cross-user).
func (s *APIKeyService) GetKey(ctx context.Context, userIDStr, keyIDStr string) (*APIKeyDTO, error) {
	uID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid user id")
	}
	kID, err := uuid.Parse(keyIDStr)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid key id")
	}
	key, err := s.Repo.GetAPIKeyByID(ctx, kID)
	if err != nil || key == nil {
		return nil, betoerrors.New(betoerrors.CodeDBNotFound, "api key not found")
	}
	if key.UserID != uID {
		// Ne jamais divulguer l'existence d'une clé d'un autre user.
		return nil, betoerrors.New(betoerrors.CodeDBNotFound, "api key not found")
	}
	dto := toAPIKeyDTO(*key)
	return &dto, nil
}

// ListAudit retourne une vue "audit" de toutes les clés du user
// (usage_count, last_used_at, status calculé). Triée par CreatedAt DESC.
func (s *APIKeyService) ListAudit(ctx context.Context, userIDStr string) ([]APIKeyAuditEntry, error) {
	uID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid user id")
	}
	keys, err := s.Repo.ListUserAPIKeys(ctx, uID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]APIKeyAuditEntry, 0, len(keys))
	for _, k := range keys {
		status := "active"
		if k.RevokedAt != nil || !k.IsActive {
			status = "revoked"
		} else if k.ExpiresAt != nil && now.After(*k.ExpiresAt) {
			status = "expired"
		}
		out = append(out, APIKeyAuditEntry{
			ID:         k.ID.String(),
			Name:       k.Name,
			Prefix:     k.Prefix,
			Kind:       k.Kind,
			UsageCount: k.UsageCount,
			LastUsedAt: k.LastUsedAt,
			CreatedAt:  k.CreatedAt,
			RevokedAt:  k.RevokedAt,
			ExpiresAt:  k.ExpiresAt,
			Status:     status,
		})
	}
	return out, nil
}

func toAPIKeyDTO(k domain.APIKey) APIKeyDTO {
	return APIKeyDTO{
		ID:         k.ID.String(),
		Name:       k.Name,
		Prefix:     k.Prefix,
		Kind:       k.Kind,
		Scopes:     []string(k.Scopes),
		UsageCount: k.UsageCount,
		ExpiresAt:  k.ExpiresAt,
		RevokedAt:  k.RevokedAt,
		LastUsedAt: k.LastUsedAt,
		CreatedAt:  k.CreatedAt,
	}
}

// RevokeKey marque la clé comme révoquée ET propage la révocation dans le cache
// (TTL 30j — couvre la fenêtre où une clé compromise pourrait être utilisée).
//
// Ordre des opérations :
//  1. Lookup de la clé par ID pour récupérer le prefix ET valider l'ownership
//     (un user ne peut pas révoquer la clé d'un autre).
//  2. Invalidation du cache EN PREMIER — même si le DELETE DB échoue après,
//     on empêche l'utilisation en fast-path (pattern de sécurité "fail-safe").
//  3. Suppression en base.
func (s *APIKeyService) RevokeKey(ctx context.Context, userIDStr, keyIDStr string) error {
	uID, err := uuid.Parse(userIDStr)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid user id")
	}
	kID, err := uuid.Parse(keyIDStr)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid key id")
	}

	// 1. Lookup pour récupérer prefix + vérifier ownership.
	key, err := s.Repo.GetAPIKeyByID(ctx, kID)
	if err != nil || key == nil {
		return betoerrors.New(betoerrors.CodeDBNotFound, "api key not found")
	}
	if key.UserID != uID {
		// Ne pas divulguer l'existence — même message que "not found".
		return betoerrors.New(betoerrors.CodeDBNotFound, "api key not found")
	}

	// 2. Invalidation cache AVANT le DELETE DB — si le DELETE rate, au moins
	//    le middleware rejettera cette clé via RevokeAPIKeyPrefix.
	if s.cache != nil && key.Prefix != "" {
		if err := s.cache.RevokeAPIKeyPrefix(ctx, key.Prefix, apiKeyRevocationTTL); err != nil {
			// Log mais continue : la suppression DB reste nécessaire.
			// Note : pas de logger dans le service, on laisse silent.
			_ = err
		}
	}

	// 3. Suppression DB.
	return s.Repo.RevokeAPIKey(ctx, kID, uID)
}

// APIKeyValidation : résultat d'une validation (retourné au middleware).
type APIKeyValidation struct {
	UserID    string
	ProjectID string
	Kind      string
	Scopes    []string
	KeyID     string
}

// ValidateKey : extrait le prefix du raw, lookup DB, compare hash en constant-time.
// À utiliser depuis le middleware auth (ou depuis un endpoint d'introspection).
func (s *APIKeyService) ValidateKey(ctx context.Context, rawKey string) (*APIKeyValidation, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return nil, errors.New("api key is required")
	}
	prefix := extractPrefix(rawKey)
	if prefix == "" {
		return nil, errors.New("invalid api key format")
	}

	// Fast-reject via cache revocation.
	if s.cache != nil {
		if revoked, _ := s.cache.IsAPIKeyPrefixRevoked(ctx, prefix); revoked {
			return nil, errors.New("api key has been revoked")
		}
	}

	key, err := s.Repo.GetAPIKeyByPrefix(ctx, prefix)
	if err != nil || key == nil {
		return nil, errors.New("invalid api key")
	}
	if !key.IsActive || key.RevokedAt != nil {
		return nil, errors.New("api key has been revoked")
	}
	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return nil, errors.New("api key has expired")
	}

	expected := hashKey(rawKey)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(key.KeyHash)) != 1 {
		return nil, errors.New("invalid api key")
	}

	return &APIKeyValidation{
		UserID:    key.UserID.String(),
		ProjectID: key.ProjectID.String(),
		Kind:      key.Kind,
		Scopes:    []string(key.Scopes),
		KeyID:     key.ID.String(),
	}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

// generateKey : produit (prefix, fullKey) où fullKey = prefix + "_" + secret.
// Format : "km_<env>_<12 hex>_<40 hex>"
func (s *APIKeyService) generateKey() (string, string, error) {
	env := s.envTag
	if env == "" {
		env = apiKeyDefaultEnvTag
	}
	prefixBytes := make([]byte, apiKeyPrefixRandomBytes)
	if _, err := rand.Read(prefixBytes); err != nil {
		return "", "", err
	}
	secretBytes := make([]byte, apiKeySecretBytes)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", err
	}
	prefix := fmt.Sprintf("km_%s_%s", env, hex.EncodeToString(prefixBytes))
	full := fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(secretBytes))
	return prefix, full, nil
}

// extractPrefix : isole la partie publique d'une clé "prefix_secret".
func extractPrefix(raw string) string {
	// Le prefix a 3 segments séparés par "_", suivi du secret.
	// ex: "km_prod_ab12cd34ef56_<secret>"
	parts := strings.SplitN(raw, "_", 4)
	if len(parts) < 4 {
		return ""
	}
	return strings.Join(parts[:3], "_")
}

func hashKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

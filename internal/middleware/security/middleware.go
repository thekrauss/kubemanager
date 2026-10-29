// Package security orchestre l'auth HTTP via MiddlewareManager.
//
// Flux d'authentification (pattern gophercart) :
//  1. publicPaths → skip
//  2. X-API-Key header OU Authorization: ApiKey → handleAPIKey
//  3. Authorization: Bearer → handleBearer (JWT JWKS + cache claims + blacklist JTI)
//  4. Rien → 401
//
// Contexte injecté en cas de succès : userID, email, roles, permissions,
// tenantID, authMethod ("bearer" ou "apikey"), scopes.
package security

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/thekrauss/kubemanager/internal/iam"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	authcache "github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/core/workerpool"
	"github.com/thekrauss/kubemanager/internal/middleware"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
)

// maxBearerCacheTTL borne la durée pendant laquelle un cache Bearer peut être
// utilisé. Même si le token Keycloak est valide 15min, on expire en cache à
// 5min max pour que les révocations DB (password change, admin force-logout)
// se propagent rapidement.
const maxBearerCacheTTL = 5 * time.Minute

// usageUpdateTimeout : budget pour l'update asynchrone de last_used_at/usage_count.
// Détaché du context de la requête HTTP (sinon cancellé au retour).
const usageUpdateTimeout = 5 * time.Second

// publicPaths : routes qui ne passent pas par l'auth.
//
// Matching avec boundary check (pas HasPrefix naïf) — évite qu'un path type
// "/api/v1/health-sneaky-endpoint" passe public à cause du prefix "/api/v1/health".
// Règle : match si path == pub OU path commence par pub avec un séparateur "/"
// (ou si pub se termine déjà par "/", dans ce cas HasPrefix suffit).
var publicPaths = []string{
	"/api/v1/health",
	"/healthz",
	"/metrics",
	"/swagger/",
	// Les routes de l'API déclarées Public() s'ajoutent via SetPublicRoutes.
}

// isPublicPath teste si `path` est dans publicPaths avec un matching propre
// (exact OU préfixe + séparateur "/" OU préfixe se terminant par "/").
func isPublicPath(path string) bool {
	for _, pub := range publicPaths {
		if path == pub {
			return true
		}
		if strings.HasSuffix(pub, "/") && strings.HasPrefix(path, pub) {
			return true
		}
		if strings.HasPrefix(path, pub+"/") {
			return true
		}
	}
	return false
}

// UsageIncrementer : interface minimale exposée par authSvc.UsageAccumulator.
// Permet d'injecter un mock dans les tests du middleware.
type UsageIncrementer interface {
	Incr(keyID uuid.UUID)
}

type MiddlewareManager struct {
	config         *configs.GlobalConfig
	verifier       *middleware.Verifier
	logger         *zap.SugaredLogger
	authRepo       repository.AuthRepository
	audit          *middleware.AuditLogger
	authCache      authcache.AuthCache
	tenantResolver *middleware.TenantResolver
	asyncPool      *workerpool.Pool // fix P3 : bornage goroutines pour DB async
	usageIncr      UsageIncrementer // fix P4 : batch des usage_count
	authorizer     Authorizer
	publicRoutes   map[string]bool
	keyAuth        KeyAuthenticator
}

func NewMiddlewareManager(
	cfg *configs.GlobalConfig,
	verifier *middleware.Verifier,
	logger *zap.SugaredLogger,
	authRepo repository.AuthRepository,
	audit *middleware.AuditLogger,
	authCache authcache.AuthCache,
	tenantResolver *middleware.TenantResolver,
	asyncPool *workerpool.Pool,
	usageIncr UsageIncrementer,
) *MiddlewareManager {
	return &MiddlewareManager{
		config:         cfg,
		verifier:       verifier,
		logger:         logger.With("component", "auth-middleware"),
		authRepo:       authRepo,
		audit:          audit,
		authCache:      authCache,
		tenantResolver: tenantResolver,
		asyncPool:      asyncPool,
		usageIncr:      usageIncr,
	}
}

// AuthMiddleware : dispatch Bearer / APIKey / public.
// C'est le handler racine monté sur l'engine Gin.
func (m *MiddlewareManager) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isPublicPath(c.Request.URL.Path) || m.publicRoutes[c.FullPath()] {
			c.Next()
			return
		}

		// 1. Priorité à X-API-Key (ou Authorization: ApiKey) — CLI, backend-to-backend
		if rawKey := extractAPIKey(c); rawKey != "" {
			if iam.LooksLikeAPIKeyV2(rawKey) {
				m.handleAPIKeyV2(c, rawKey)
				return
			}
			m.handleAPIKey(c, rawKey)
			return
		}

		// 2. Fallback : Authorization: Bearer <jwt> (ou une clé v2 en Bearer)
		tokenString := extractBearerToken(c.GetHeader("Authorization"))
		if iam.LooksLikeAPIKeyV2(tokenString) {
			m.handleAPIKeyV2(c, tokenString)
			return
		}
		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header (Bearer or X-API-Key)"})
			return
		}
		m.handleBearer(c, tokenString)
	}
}

// submitAsync : pousse une tâche fire-and-forget dans le worker pool borné.
// Fallback sur goroutine classique (avec ctx détaché + timeout) si pas de pool.
// Retourne false si la queue est pleine (appelant peut logger).
func (m *MiddlewareManager) submitAsync(task func(ctx context.Context)) bool {
	if m.asyncPool != nil {
		return m.asyncPool.Submit(workerpool.Task(task))
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), usageUpdateTimeout)
		defer cancel()
		task(ctx)
	}()
	return true
}

// handleBearer : valide un JWT Keycloak, check blacklist JTI + revocation DB, cache claims.
//
// IMPORTANT : même sur cache hit, on re-vérifie blacklist JTI + revocation DB.
// Le cache n'évite QUE la vérification cryptographique JWKS+signature, qui est
// le coût dominant. Les checks de révocation sont indispensables pour que
// logout / password change / admin force-logout prennent effet sans attendre
// l'expiration naturelle du token (potentiellement 15+ min).
func (m *MiddlewareManager) handleBearer(c *gin.Context, tokenString string) {
	ctx := c.Request.Context()

	// 1. Tentative cache hit : évite JWKS + signature + parse.
	var claims authcache.AccessTokenClaims
	var cacheHit bool
	if m.authCache != nil {
		if cached, ok, _ := m.authCache.GetAccessTokenClaims(ctx, tokenString); ok && cached.ExpiresAt > time.Now().Unix() {
			claims = cached
			cacheHit = true
		}
	}

	// 2. Cache miss → vérification cryptographique complète.
	var fullClaims *middleware.Claims
	if !cacheHit {
		verified, err := m.verifier.Verify(tokenString)
		if err != nil {
			m.logger.Debugw("token verification failed", "error", err)
			if m.audit != nil {
				m.audit.LogAuthEvent(c, middleware.AuditAuthFailed, zap.String("reason", "invalid_token"))
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		fullClaims = verified
		claims = authcache.AccessTokenClaims{
			UserID:   verified.Sub,
			Email:    verified.Email,
			TenantID: verified.TenantID,
			Roles:    verified.Roles,
			Scopes:   splitScope(verified.Scope),
			JTI:      verified.ID,
		}
		if verified.ExpiresAt != nil {
			claims.ExpiresAt = verified.ExpiresAt.Unix()
		}
	}

	// 3. Blacklist JTI — toujours vérifié (cache hit OU miss).
	// Couvre le cas du logout immédiat.
	if m.authCache != nil && claims.JTI != "" {
		if blacklisted, _ := m.authCache.IsAccessTokenJTIBlacklisted(ctx, claims.JTI); blacklisted {
			if m.audit != nil {
				m.audit.LogAuthEvent(c, middleware.AuditTokenRevoked, zap.String("jti", claims.JTI))
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
			return
		}
	}

	// 4. Revocation DB — toujours vérifié (cache hit OU miss).
	// Couvre password change, admin force-logout, etc.
	// C'est un index-only query → rapide. Le cache sauve JWKS + signature (bien plus cher).
	tokenHash := hashToken(tokenString)
	if revoked, err := m.authRepo.IsTokenRevoked(ctx, tokenHash); err != nil {
		m.logger.Errorw("token revocation check failed", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authentication check failed"})
		return
	} else if revoked {
		if m.audit != nil {
			m.audit.LogAuthEvent(c, middleware.AuditTokenRevoked)
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
		return
	}

	// 5. Store cache (uniquement sur cache miss, pour pas réécrire à chaque hit).
	// TTL plafonné à maxBearerCacheTTL pour borner la latence de propagation
	// d'une révocation DB : même si le check DB est toujours fait, réduire le
	// TTL limite la fenêtre où un token compromis pourrait être utilisé.
	if !cacheHit && m.authCache != nil && fullClaims != nil && fullClaims.ExpiresAt != nil {
		ttl := time.Until(fullClaims.ExpiresAt.Time)
		if ttl > maxBearerCacheTTL {
			ttl = maxBearerCacheTTL
		}
		if ttl > 0 {
			_ = m.authCache.SetAccessTokenClaims(ctx, tokenString, claims, ttl)
		}
	}

	// 6. Injection dans le contexte Gin.
	m.applyBearerClaims(c, claims)
	if fullClaims != nil && len(fullClaims.Permissions) > 0 {
		c.Set("permissions", fullClaims.Permissions)
	}

	// Tenant resolver : override possible depuis l'URL/header/host (sinon on garde celui du JWT).
	if m.tenantResolver != nil {
		if tid, ok := m.tenantResolver.Resolve(c.Request); ok {
			c.Set("tenantID", tid)
		}
	}

	if m.audit != nil {
		m.audit.LogAuthEvent(c, middleware.AuditAuthSuccess, zap.String("auth_method", "bearer"))
	}
	c.Next()
}

// handleAPIKey : valide une API key (prefix + bcrypt/hash), cache validation 30s,
// check prefix revocation, injecte le context user/project/scopes.
func (m *MiddlewareManager) handleAPIKey(c *gin.Context, rawKey string) {
	ctx := c.Request.Context()

	prefix := extractAPIKeyPrefix(rawKey)
	if prefix == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key format"})
		return
	}

	// Fast-reject : prefix révoqué récemment (propage la révocation avant expiration du cache validation).
	if m.authCache != nil {
		if revoked, _ := m.authCache.IsAPIKeyPrefixRevoked(ctx, prefix); revoked {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key has been revoked"})
			return
		}
		// Cache validation (clé = raw key hash interne).
		if v, ok, _ := m.authCache.GetAPIKeyValidation(ctx, rawKey); ok {
			m.applyAPIKeyValidation(c, v)
			c.Next()
			return
		}
	}

	key, err := m.authRepo.GetAPIKeyByPrefix(ctx, prefix)
	if err != nil || key == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
		return
	}
	if !key.IsActive || key.RevokedAt != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key has been revoked"})
		return
	}
	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key has expired"})
		return
	}
	// Constant-time comparison : évite les timing attacks sur le hash.
	expected := utils.HashAPIKey(rawKey)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(key.KeyHash)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
		return
	}

	// Fix P4 : accumulator in-memory + flush batch → pas de hot row DB.
	// Si accumulator non câblé (tests), fallback sur le pool async.
	if m.usageIncr != nil {
		m.usageIncr.Incr(key.ID)
	} else {
		keyID := key.ID
		submitted := m.submitAsync(func(ctx context.Context) {
			if err := m.authRepo.UpdateAPIKeyUsage(ctx, keyID); err != nil {
				m.logger.Warnw("failed to update api key usage", "key_id", keyID, "error", err)
			}
		})
		if !submitted {
			m.logger.Warnw("api key usage update dropped (pool full)", "key_id", keyID)
		}
	}

	validation := authcache.APIKeyValidation{
		UserID: key.UserID.String(),
		ServiceAccountID: func() string {
			if key.ServiceAccountID == nil {
				return ""
			}
			return key.ServiceAccountID.String()
		}(),
		TenantID: key.ProjectID.String(),
		Kind:     key.Kind,
		Scopes:   []string(key.Scopes),
	}
	if m.authCache != nil {
		_ = m.authCache.SetAPIKeyValidation(ctx, rawKey, validation, 30*time.Second)
	}
	m.applyAPIKeyValidation(c, validation)
	c.Set("apiKeyID", key.ID.String())

	if m.audit != nil {
		m.audit.LogAuthEvent(c, middleware.AuditAPIKeyUsed,
			zap.String("api_key_id", key.ID.String()),
			zap.String("prefix", prefix),
		)
	}
	c.Next()
}

// applyBearerClaims injecte les claims JWT dans le contexte Gin.
func (m *MiddlewareManager) applyBearerClaims(c *gin.Context, claims authcache.AccessTokenClaims) {
	c.Set("userID", claims.UserID)
	c.Set("email", claims.Email)
	if claims.TenantID != "" {
		c.Set("tenantID", claims.TenantID)
	}
	if len(claims.Roles) > 0 {
		c.Set("roles", claims.Roles)
	}
	if len(claims.Scopes) > 0 {
		c.Set("scopes", claims.Scopes)
	}
	c.Set("authMethod", "bearer")
}

// applyAPIKeyValidation injecte les infos API key dans le contexte Gin.
func (m *MiddlewareManager) applyAPIKeyValidation(c *gin.Context, v authcache.APIKeyValidation) {
	if v.ServiceAccountID != "" {
		// Clé legacy rattachée à un compte de service : elle n'agit plus
		// comme l'utilisateur (pas de userID dans le contexte).
		c.Set(ctxPrincipal, "serviceAccount:"+v.ServiceAccountID)
	} else {
		c.Set("userID", v.UserID)
	}
	if v.TenantID != "" {
		c.Set("tenantID", v.TenantID)
		c.Set("projectID", v.TenantID)
	}
	if len(v.Scopes) > 0 {
		c.Set("scopes", v.Scopes)
		c.Set("apiKeyScopes", v.Scopes) // compat avec RequireAPIKeyScope existant
	}
	if v.Kind != "" {
		c.Set("apiKeyKind", v.Kind)
	}
	c.Set("authMethod", "apikey")
}

// RequireAPIKeyKind : bloque la route sauf si le caller a été authentifié
// via une API key dont le Kind matche l'un des `allowed`.
//
// Usage typique — routes internes exposées à des agents/clusters K8s :
//
//	RealtimeGroup.Use(mgr.RequireAPIKeyKind("cluster"))
//
// Placé APRÈS AuthMiddleware : le context a déjà `authMethod` + `apiKeyKind`.
func (m *MiddlewareManager) RequireAPIKeyKind(allowed ...string) gin.HandlerFunc {
	set := make(map[string]struct{}, len(allowed))
	for _, k := range allowed {
		set[k] = struct{}{}
	}
	return func(c *gin.Context) {
		if c.GetString("authMethod") != "apikey" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "api key authentication required"})
			return
		}
		kind := c.GetString("apiKeyKind")
		if _, ok := set[kind]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":         "api key kind not allowed for this route",
				"required_kind": allowed,
				"got_kind":      kind,
			})
			return
		}
		c.Next()
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func extractBearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// extractAPIKey : X-API-Key ou Authorization: ApiKey <key>.
func extractAPIKey(c *gin.Context) string {
	if key := strings.TrimSpace(c.GetHeader("X-API-Key")); key != "" {
		return key
	}
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "ApiKey") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// extractAPIKeyPrefix : extrait le prefix d'une clé au format "km_<env>_<body>"
// ou "<prefix>_<secret>". Fallback : les 7 premiers caractères (pattern legacy).
func extractAPIKeyPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Format structuré "prefix_secret" (ex: "km_prod_ab12_secret...")
	if idx := strings.LastIndex(raw, "_"); idx > 0 {
		candidate := raw[:idx]
		if len(candidate) >= 3 && len(candidate) <= 32 {
			return candidate
		}
	}
	// Legacy : les 7 premiers caractères.
	if len(raw) >= 7 {
		return raw[:7]
	}
	return raw
}

func splitScope(scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil
	}
	parts := strings.Fields(scope)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

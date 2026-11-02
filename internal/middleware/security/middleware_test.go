package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	authcache "github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
)

// ── Helpers : tests unitaires purs ───────────────────────────────────────

func TestExtractBearerToken(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"Bearer abc.def.ghi", "abc.def.ghi"},
		{"bearer abc", "abc"},   // case-insensitive
		{"BEARER  abc", "abc"},  // extra spaces trimmed
		{"ApiKey foo", ""},      // wrong scheme
		{"abc.def.ghi", ""},     // no scheme
		{"Bearer", ""},          // no token
	}
	for _, c := range cases {
		assert.Equal(t, c.want, extractBearerToken(c.in), "input=%q", c.in)
	}
}

func TestExtractAPIKeyPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"km_prod_ab12cd34ef56_abcdef1234567890", "km_prod_ab12cd34ef56"},
		{"km_dev_deadbeef_secretdata", "km_dev_deadbeef"},
		{"legacy7chars", "legacy7"}, // fallback legacy
	}
	for _, c := range cases {
		assert.Equal(t, c.want, extractAPIKeyPrefix(c.in), "input=%q", c.in)
	}
}

func TestSplitScope(t *testing.T) {
	assert.Nil(t, splitScope(""))
	assert.Equal(t, []string{"a", "b", "c"}, splitScope("a b c"))
	assert.Equal(t, []string{"a", "b"}, splitScope("  a   b  "))
}

// ── Fake AuthRepository : n'implémente que ce que le middleware utilise ─

type fakeAuthRepo struct {
	repository.AuthRepository // embed nil → panic si méthode non overridée appelée
	keyByPrefix               map[string]*domain.APIKey
	tokenRevoked              map[string]bool
}

func (f *fakeAuthRepo) GetAPIKeyByPrefix(ctx context.Context, prefix string) (*domain.APIKey, error) {
	if k, ok := f.keyByPrefix[prefix]; ok {
		return k, nil
	}
	return nil, nil
}

func (f *fakeAuthRepo) UpdateAPIKeyUsage(ctx context.Context, keyID uuid.UUID) error {
	return nil
}

func (f *fakeAuthRepo) IsTokenRevoked(ctx context.Context, tokenHash string) (bool, error) {
	return f.tokenRevoked[tokenHash], nil
}

// ── Setup helper ─────────────────────────────────────────────────────────

func newTestManager(t *testing.T, repo repository.AuthRepository) (*MiddlewareManager, authcache.AuthCache) {
	t.Helper()
	ac, err := authcache.NewRistrettoAuthCache()
	require.NoError(t, err)

	logger := zap.NewNop().Sugar()
	cfg := &configs.GlobalConfig{}
	m := NewMiddlewareManager(cfg, nil, logger, repo, nil, ac, nil, nil, nil)
	return m, ac
}

func newTestAPIKey(t *testing.T, rawKey string, scopes []string, active bool) *domain.APIKey {
	t.Helper()
	hash := utils.HashAPIKey(rawKey)
	return &domain.APIKey{
		ID:       uuid.New(),
		UserID:   uuid.New(),
		Prefix:   extractAPIKeyPrefix(rawKey),
		KeyHash:  hash,
		Scopes:   pq.StringArray(scopes),
		IsActive: active,
		Kind:     domain.APIKeyKindCLI,
	}
}

// ── Tests APIKey flow ────────────────────────────────────────────────────

func TestAuthMiddleware_APIKey_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rawKey := "km_prod_ab12cd34ef56_" + uuid.New().String()
	apiKey := newTestAPIKey(t, rawKey, []string{"project:view"}, true)

	repo := &fakeAuthRepo{
		keyByPrefix:  map[string]*domain.APIKey{apiKey.Prefix: apiKey},
		tokenRevoked: map[string]bool{},
	}
	mgr, _ := newTestManager(t, repo)

	var capturedUserID string
	var capturedScopes []string
	var capturedAuthMethod string
	r := gin.New()
	r.Use(mgr.AuthMiddleware())
	r.GET("/protected", func(c *gin.Context) {
		capturedUserID = c.GetString("userID")
		capturedScopes = c.GetStringSlice("scopes")
		capturedAuthMethod = c.GetString("authMethod")
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, apiKey.UserID.String(), capturedUserID)
	assert.Equal(t, []string{"project:view"}, capturedScopes)
	assert.Equal(t, "apikey", capturedAuthMethod)
}

func TestAuthMiddleware_APIKey_Revoked_ViaCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rawKey := "km_prod_deadbeef0000_" + uuid.New().String()
	apiKey := newTestAPIKey(t, rawKey, []string{"project:view"}, true)
	repo := &fakeAuthRepo{keyByPrefix: map[string]*domain.APIKey{apiKey.Prefix: apiKey}}
	mgr, cache := newTestManager(t, repo)

	// Simule une révocation : prefix ajouté au cache des prefixes révoqués.
	err := cache.RevokeAPIKeyPrefix(context.Background(), apiKey.Prefix, time.Hour)
	require.NoError(t, err)
	// Laisser Ristretto digérer l'écriture (async buffer).
	time.Sleep(20 * time.Millisecond)

	r := gin.New()
	r.Use(mgr.AuthMiddleware())
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "revoked")
}

func TestAuthMiddleware_APIKey_Inactive(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rawKey := "km_prod_inactive0000_" + uuid.New().String()
	apiKey := newTestAPIKey(t, rawKey, nil, false) // IsActive=false
	repo := &fakeAuthRepo{keyByPrefix: map[string]*domain.APIKey{apiKey.Prefix: apiKey}}
	mgr, _ := newTestManager(t, repo)

	r := gin.New()
	r.Use(mgr.AuthMiddleware())
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_APIKey_HashMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rawKey := "km_prod_ab12cd34ef56_realsecret"
	apiKey := newTestAPIKey(t, rawKey, nil, true)
	repo := &fakeAuthRepo{keyByPrefix: map[string]*domain.APIKey{apiKey.Prefix: apiKey}}
	mgr, _ := newTestManager(t, repo)

	r := gin.New()
	r.Use(mgr.AuthMiddleware())
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	// Même prefix, mais secret différent → hash check fail.
	req.Header.Set("X-API-Key", "km_prod_ab12cd34ef56_fakesecret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_APIKey_Expired(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rawKey := "km_prod_expired00000_" + uuid.New().String()
	apiKey := newTestAPIKey(t, rawKey, nil, true)
	past := time.Now().Add(-1 * time.Hour)
	apiKey.ExpiresAt = &past
	repo := &fakeAuthRepo{keyByPrefix: map[string]*domain.APIKey{apiKey.Prefix: apiKey}}
	mgr, _ := newTestManager(t, repo)

	r := gin.New()
	r.Use(mgr.AuthMiddleware())
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "expired")
}

func TestAuthMiddleware_NoCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := &fakeAuthRepo{}
	mgr, _ := newTestManager(t, repo)

	r := gin.New()
	r.Use(mgr.AuthMiddleware())
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_PublicPath_Bypass(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := &fakeAuthRepo{}
	mgr, _ := newTestManager(t, repo)

	r := gin.New()
	r.Use(mgr.AuthMiddleware())
	r.GET("/api/v1/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// Fix H5 : publicPaths boundary check — prefix confusion doit être rejetée.
func TestIsPublicPath_BoundaryCheck(t *testing.T) {
	// Chemins légitimes (doivent être publics).
	publicCases := []string{
		"/api/v1/health",
		"/healthz",
		"/metrics",
		"/swagger/",
		"/swagger/index.html", // prefix qui finit par "/"
	}
	for _, p := range publicCases {
		assert.True(t, isPublicPath(p), "doit être public: %s", p)
	}

	// Chemins piégeux — le fix doit les rejeter.
	privateCases := []string{
		"/api/v1/health-admin",                  // typosquatting
		"/api/v1/healthcheck",                   // concatenation
		"/kmanager/v1/identity/registerbypass",  // concat sur register
		"/kmanager/v1/identity/loginfoo",        // concat sur login
		"/kmanager/v1/identity/social",          // prefix sans / final
		"/kmanager/v1/identity/oidc",            // prefix sans / final
		"/swagger",                              // sans / final
		"/swaggerxss",                           // typosquatting
		"/metrics-admin",                        // typosquatting
	}
	for _, p := range privateCases {
		assert.False(t, isPublicPath(p), "ne doit PAS être public: %s", p)
	}
}

// Les routes de l'API déclarées Public() sont reconnues par leur chemin gin
// (paramètres compris) ; un chemin voisin non déclaré reste protégé.
func TestAuthMiddleware_DeclaredPublicRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m, _ := newTestManager(t, &fakeAuthRepo{})
	m.SetPublicRoutes(map[string]bool{"/kmanager/v1/identity/social/:provider": true})

	r := gin.New()
	r.Use(m.AuthMiddleware())
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.GET("/kmanager/v1/identity/social/:provider", ok)
	r.GET("/kmanager/v1/identity/validate", ok)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kmanager/v1/identity/social/google", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kmanager/v1/identity/validate", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// jtiCache : contrôle de révocation en panne ou positif.
type jtiCache struct {
	authcache.AuthCache
	blacklisted bool
	err         error
}

func (j jtiCache) IsAccessTokenJTIBlacklisted(context.Context, string) (bool, error) {
	return j.blacklisted, j.err
}

// Révocation invérifiable : refus, un jeton révoqué ne passe pas pendant la panne.
func TestRefuseRevokedJTI(t *testing.T) {
	cases := []struct {
		name    string
		cache   jtiCache
		refused bool
		status  int
	}{
		{"valide", jtiCache{}, false, http.StatusOK},
		{"révoqué", jtiCache{blacklisted: true}, true, http.StatusUnauthorized},
		{"cache en panne", jtiCache{err: context.DeadlineExceeded}, true, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			m := &MiddlewareManager{authCache: tc.cache, logger: zap.NewNop().Sugar()}
			assert.Equal(t, tc.refused, m.refuseRevokedJTI(c, "jti"))
			assert.Equal(t, tc.status, w.Code)
		})
	}
}

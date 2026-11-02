// Tests d'intégration du STS : vrai Postgres (voir internal/iam/e2e_test.go,
// à lancer avec -p 1), ignorés sans IAM_E2E.
package sts_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/iam/sts"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	"github.com/thekrauss/kubemanager/internal/modules/iamapi"
)

const (
	dsn    = "postgres://postgres:pw@127.0.0.1:55433/postgres?sslmode=disable"
	issuer = "https://api.test/kmanager/v1/iam"
)

var kek = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func db(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("IAM_E2E") == "" {
		t.Skip("IAM_E2E not set")
	}
	d, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	return d
}

// Rotation : une clé tout de suite, la suivante publiée un jour avant de
// servir, l'ancienne retirée quand ses jetons ont expiré.
func TestE2E_SigningKeyRotation(t *testing.T) {
	d := db(t)
	ctx := context.Background()
	require.NoError(t, d.Exec(`DELETE FROM iam_signing_keys`).Error)

	_, err := sts.NewKeyRing(d, "trop-court")
	assert.ErrorIs(t, err, sts.ErrInvalidKEK)

	clock := time.Now()
	ring, err := sts.NewKeyRing(d, kek)
	require.NoError(t, err)
	ring.SetNow(func() time.Time { return clock })
	svc := sts.NewService(issuer, ring, d)
	svc.SetNow(func() time.Time { return clock })

	require.NoError(t, ring.Rotate(ctx))
	require.NoError(t, ring.Rotate(ctx), "idempotent")
	jwks := ring.JWKS()
	require.Len(t, jwks.Keys, 1)
	first := jwks.Keys[0].Kid

	tok1, _, err := svc.Mint("serviceAccount:"+iam.Principal(uuid.NewString()), sts.MethodJWTBearer, "", time.Hour)
	require.NoError(t, err)

	// J+30 : nouvelle clé publiée, l'ancienne signe encore.
	clock = clock.Add(sts.RotationPeriod)
	require.NoError(t, ring.Rotate(ctx))
	assert.Len(t, ring.JWKS().Keys, 2)
	tok2, _, err := svc.Mint("serviceAccount:x", sts.MethodJWTBearer, "", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, first, kidOf(t, tok2), "la nouvelle clé n'est pas encore active")

	// J+31 : la nouvelle signe, l'ancienne est encore publiée (jetons en vol).
	clock = clock.Add(sts.PublishAhead)
	tok3, _, err := svc.Mint("serviceAccount:x", sts.MethodJWTBearer, "", time.Hour)
	require.NoError(t, err)
	assert.NotEqual(t, first, kidOf(t, tok3))
	assert.Len(t, ring.JWKS().Keys, 2)

	// Au-delà de la durée de vie des jetons : l'ancienne n'est plus publiée.
	clock = clock.Add(sts.MaxTokenTTL + 2*time.Hour)
	require.NoError(t, ring.Reload(ctx))
	assert.Len(t, ring.JWKS().Keys, 1)
	_ = tok1

	// Mauvaise KEK : les clés ne se déchiffrent pas, on ne démarre pas.
	other, err := sts.NewKeyRing(d, base64.StdEncoding.EncodeToString([]byte("ffffffffffffffffffffffffffffffff")))
	require.NoError(t, err)
	assert.Error(t, other.Reload(ctx))
}

// De bout en bout en HTTP : clé générée → assertion → /token → appel de l'API
// avec le jeton → rejeu, mauvaise audience, désactivation.
func TestE2E_JWTBearerFlow(t *testing.T) {
	d := db(t)
	ctx := context.Background()
	require.NoError(t, d.Exec(`DELETE FROM iam_signing_keys`).Error)
	engine := iam.NewEngine(&iam.GormStore{DB: d}, iam.DefaultCacheTTL)
	ring, err := sts.NewKeyRing(d, kek)
	require.NoError(t, err)
	require.NoError(t, ring.Rotate(ctx))
	svc := sts.NewService(issuer, ring, d)
	sas, err := iam.NewServiceAccountService(d, engine, []byte("integration-test-pepper-0123456789abcdef"))
	require.NoError(t, err)
	sas.TokenURI = svc.TokenURI()
	policies := &iam.PolicyService{DB: d, Engine: engine}

	awa := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	fatou := iam.UserPrincipal(uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	projectID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	project := iam.ProjectName(projectID)
	sa, err := sas.Create(ctx, awa, projectID, "ci-"+uuid.NewString()[:8], "CI", "")
	require.NoError(t, err)
	view, err := policies.Get(ctx, project)
	require.NoError(t, err)
	next := view.Policy
	next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: "roles/sites.deployer", Members: []iam.Principal{sa.Principal}})
	_, err = policies.Set(ctx, awa, project, next)
	require.NoError(t, err)

	// ── Clés ──
	_, err = sas.CreateSAKey(ctx, fatou, projectID, sa.ID, iam.CreateSAKeyRequest{})
	assert.ErrorIs(t, err, iam.ErrActAsRequired)
	_, err = sas.CreateSAKey(ctx, awa, projectID, sa.ID, iam.CreateSAKeyRequest{ExpiresInDays: 400})
	assert.ErrorIs(t, err, iam.ErrInvalidSAKeyExpiry)
	_, err = sas.CreateSAKey(ctx, awa, projectID, sa.ID, iam.CreateSAKeyRequest{PublicKeyPEM: "nope"})
	assert.ErrorIs(t, err, iam.ErrInvalidPublicKey)
	created, err := sas.CreateSAKey(ctx, awa, projectID, sa.ID, iam.CreateSAKeyRequest{DisplayName: "github"})
	require.NoError(t, err)
	require.NotNil(t, created.KeyFile)
	assert.Equal(t, svc.TokenURI(), created.KeyFile.TokenURI)
	assert.Equal(t, iam.KeyFileType, created.KeyFile.Type)
	assert.NotContains(t, created.PublicKeyPEM, "PRIVATE")
	priv := parsePriv(t, created.KeyFile.PrivateKey)

	// Clé importée (la clé privée reste chez le client).
	pubDER, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	uploaded, err := sas.CreateSAKey(ctx, awa, projectID, sa.ID, iam.CreateSAKeyRequest{
		PublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))})
	require.NoError(t, err)
	assert.Nil(t, uploaded.KeyFile, "clé importée : rien à remettre")
	assert.Equal(t, "uploaded", uploaded.Origin)

	// ── HTTP ──
	gin.SetMode(gin.TestMode)
	mw := security.NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil)
	mw.SetAuthorizer(engine)
	mw.SetTokenVerifier(svc)
	ctrl := &iamapi.Controller{STS: svc}
	r := gin.New()
	r.POST("/iam/token", ctrl.Token)
	r.GET("/iam/.well-known/jwks.json", ctrl.JWKS)
	api := r.Group("/", mw.AuthMiddleware())
	ok := func(c *gin.Context) { p, _ := security.PrincipalFromContext(c); c.String(http.StatusOK, string(p)) }
	siteID := uuid.NewString()
	api.POST("/p/:id/sites/:siteID/deploy", mw.Require(iam.SiteDeploymentsCreate, security.ProjectChild("id", "sites", "siteID")), ok)
	api.DELETE("/p/:id/sites/:siteID", mw.Require(iam.SitesDelete, security.ProjectChild("id", "sites", "siteID")), ok)

	exchange := func(assertion string) (int, map[string]any) {
		form := url.Values{"grant_type": {sts.GrantJWTBearer}, "assertion": {assertion}}
		req := httptest.NewRequest("POST", "/iam/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	callAPI := func(method, path, token string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	assertion := func(keyID string, mutate func(c jwt.MapClaims)) string {
		now := time.Now()
		c := jwt.MapClaims{"iss": string(sa.Principal), "sub": string(sa.Principal), "aud": svc.TokenURI(),
			"iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(), "jti": uuid.NewString()}
		if mutate != nil {
			mutate(c)
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, c)
		tok.Header["kid"] = keyID
		s, err := tok.SignedString(priv)
		require.NoError(t, err)
		return s
	}

	// Échange réussi, puis appel autorisé par le rôle du compte, refusé hors rôle.
	signed := assertion(created.KeyID, nil)
	code, body := exchange(signed)
	require.Equal(t, http.StatusOK, code, body)
	access := body["access_token"].(string)
	assert.Equal(t, "Bearer", body["token_type"])
	assert.InDelta(t, 3600, body["expires_in"], 5)
	assert.Equal(t, http.StatusOK, callAPI("POST", "/p/"+projectID.String()+"/sites/"+siteID+"/deploy", access))
	assert.Equal(t, http.StatusForbidden, callAPI("DELETE", "/p/"+projectID.String()+"/sites/"+siteID, access))
	assert.Equal(t, http.StatusOK, callAPI("POST", "/p/"+projectID.String()+"/sites/"+siteID+"/deploy",
		mustExchange(t, exchange, assertion(uploaded.KeyID, nil))), "clé importée")

	// Refus.
	code, body = exchange(signed)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid_grant", body["error"])
	assert.Contains(t, body["error_description"], "replay")
	for name, a := range map[string]string{
		"audience": assertion(created.KeyID, func(c jwt.MapClaims) { c["aud"] = "https://evil.test/token" }),
		"iss":      assertion(created.KeyID, func(c jwt.MapClaims) { c["iss"] = "serviceAccount:" + uuid.NewString() }),
		"expirée": assertion(created.KeyID, func(c jwt.MapClaims) {
			c["iat"] = time.Now().Add(-2 * time.Hour).Unix()
			c["exp"] = time.Now().Add(-time.Hour).Unix()
		}),
		"trop longue":  assertion(created.KeyID, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(3 * time.Hour).Unix() }),
		"sans jti":     assertion(created.KeyID, func(c jwt.MapClaims) { delete(c, "jti") }),
		"clé inconnue": assertion("deadbeef", nil),
	} {
		code, body := exchange(a)
		assert.Equal(t, http.StatusBadRequest, code, name)
		assert.Equal(t, "invalid_grant", body["error"], name)
	}
	code, body = exchange("")
	assert.Equal(t, "invalid_request", body["error"])
	form := url.Values{"grant_type": {"client_credentials"}}
	req := httptest.NewRequest("POST", "/iam/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), "unsupported_grant_type")

	// Jeton falsifié (signé par une autre clé) ou d'un autre émetteur.
	forged := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": issuer, "sub": string(sa.Principal),
		"aud": sts.Audience, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
	forged.Header["kid"] = ring.JWKS().Keys[0].Kid
	forgedStr, _ := forged.SignedString(priv)
	assert.Equal(t, http.StatusUnauthorized, callAPI("POST", "/p/"+projectID.String()+"/sites/"+siteID+"/deploy", forgedStr))

	// JWKS publié.
	req = httptest.NewRequest("GET", "/iam/.well-known/jwks.json", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), `"kty":"EC"`)

	// Désactivation : plus d'échange, et le jeton déjà émis est refusé.
	require.NoError(t, sas.DisableSAKey(ctx, projectID, sa.ID, created.KeyID))
	svc.ForgetStatus() // sinon effectif sous 30 s
	code, _ = exchange(assertion(created.KeyID, nil))
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, http.StatusUnauthorized, callAPI("POST", "/p/"+projectID.String()+"/sites/"+siteID+"/deploy", access))

	// Compte supprimé : tous ses jetons tombent.
	other := mustExchange(t, exchange, assertion(uploaded.KeyID, nil))
	require.NoError(t, sas.Delete(ctx, awa, projectID, sa.ID))
	svc.ForgetStatus()
	assert.Equal(t, http.StatusUnauthorized, callAPI("POST", "/p/"+projectID.String()+"/sites/"+siteID+"/deploy", other))
}

func mustExchange(t *testing.T, exchange func(string) (int, map[string]any), a string) string {
	t.Helper()
	code, body := exchange(a)
	require.Equal(t, http.StatusOK, code, body)
	return body["access_token"].(string)
}

func parsePriv(t *testing.T, p string) *ecdsa.PrivateKey {
	t.Helper()
	block, _ := pem.Decode([]byte(p))
	require.NotNil(t, block)
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err)
	return k.(*ecdsa.PrivateKey)
}

func kidOf(t *testing.T, tok string) string {
	t.Helper()
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	require.NoError(t, err)
	return parsed.Header["kid"].(string)
}

// Watcher sans secret longue durée : fichier de clé du compte système du
// cluster → jeton STS → routes du watcher, pour son cluster seulement.
func TestE2E_ClusterAgentKeyFile(t *testing.T) {
	d := db(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: d}, iam.DefaultCacheTTL)
	ring, err := sts.NewKeyRing(d, kek)
	require.NoError(t, err)
	require.NoError(t, ring.Rotate(ctx))
	svc := sts.NewService(issuer, ring, d)
	sas, err := iam.NewServiceAccountService(d, engine, []byte("integration-test-pepper-0123456789abcdef"))
	require.NoError(t, err)
	sas.TokenURI = svc.TokenURI()
	agents := iam.NewClusterAgents(sas)
	staff := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))

	gin.SetMode(gin.TestMode)
	mw := security.NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil)
	mw.SetAuthorizer(engine)
	mw.SetKeyAuthenticator(sas)
	mw.SetTokenVerifier(svc)
	mw.SetClusterAgents(agents)
	ctrl := &iamapi.Controller{STS: svc}
	r := gin.New()
	r.POST("/iam/token", ctrl.Token)
	api := r.Group("/", mw.AuthMiddleware())
	api.POST("/events/:clusterID", mw.RequireClusterAgent(security.ClusterParam("clusterID")), func(c *gin.Context) { c.Status(http.StatusOK) })
	call := func(path, token string) int {
		req := httptest.NewRequest("POST", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	tokenFor := func(f *iam.KeyFile) string {
		t.Helper()
		now := time.Now()
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": f.Principal, "sub": f.Principal, "aud": f.TokenURI,
			"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": uuid.NewString()})
		tok.Header["kid"] = f.KeyID
		signed, err := tok.SignedString(parsePriv(t, f.PrivateKey))
		require.NoError(t, err)
		form := url.Values{"grant_type": {sts.GrantJWTBearer}, "assertion": {signed}}
		req := httptest.NewRequest("POST", "/iam/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body["access_token"].(string)
	}

	cluster := "k3s-" + uuid.NewString()[:8]
	_, err = agents.CreateKey(ctx, staff, iam.CreateClusterKeyRequest{ClusterID: cluster, Type: "ssh"})
	assert.ErrorIs(t, err, iam.ErrInvalidClusterKeyType)
	_, err = agents.CreateKey(ctx, staff, iam.CreateClusterKeyRequest{ClusterID: cluster, ExpiresInDays: 200})
	assert.ErrorIs(t, err, iam.ErrInvalidClusterKeyDays, "clé API : 90 jours max")

	// Passage d'une clé API à un fichier de clé par rotation.
	apiKey, err := agents.CreateKey(ctx, staff, iam.CreateClusterKeyRequest{ClusterID: cluster})
	require.NoError(t, err)
	kf, err := agents.Rotate(ctx, staff, apiKey.KeyID, iam.RotateClusterKeyRequest{Type: iam.ClusterKeyFile})
	require.NoError(t, err)
	require.NotNil(t, kf.KeyFile)
	assert.Empty(t, kf.Key)
	assert.Equal(t, cluster, kf.KeyFile.ClusterID)
	assert.Equal(t, "key_file", kf.Format)
	assert.Equal(t, apiKey.ServiceAccountID, kf.ServiceAccountID, "même compte système")

	access := tokenFor(kf.KeyFile)
	assert.Equal(t, http.StatusOK, call("/events/"+cluster, access))
	assert.Equal(t, http.StatusForbidden, call("/events/autre-cluster", access), "jeton d'un autre cluster")

	list, err := agents.List(ctx)
	require.NoError(t, err)
	var listed bool
	for _, k := range list {
		listed = listed || (k.KeyID == kf.KeyID && k.Format == "key_file" && k.Status == "active")
	}
	assert.True(t, listed)

	// Rotation planifiée d'un fichier de clé : nouveau fichier, l'ancien vit la grâce.
	kf2, err := agents.Rotate(ctx, staff, kf.KeyID, iam.RotateClusterKeyRequest{GraceDays: 1})
	require.NoError(t, err)
	require.NotNil(t, kf2.KeyFile, "le type est conservé")
	assert.Equal(t, http.StatusOK, call("/events/"+cluster, access))

	// Rotation immédiate (clé compromise) : les jetons déjà émis tombent aussi.
	access2 := tokenFor(kf2.KeyFile)
	_, err = agents.Rotate(ctx, staff, kf2.KeyID, iam.RotateClusterKeyRequest{Immediate: true, Type: iam.ClusterKeyFile})
	require.NoError(t, err)
	svc.ForgetStatus()
	assert.Equal(t, http.StatusUnauthorized, call("/events/"+cluster, access2))

	// Révocation.
	require.NoError(t, agents.Revoke(ctx, kf.KeyID))
	svc.ForgetStatus()
	assert.Equal(t, http.StatusUnauthorized, call("/events/"+cluster, access))
}

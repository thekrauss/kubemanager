package sts_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/iam/sts"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	"github.com/thekrauss/kubemanager/internal/modules/iamapi"
)

// Faux émetteur OIDC (rôle de GitHub Actions) : découverte + JWKS en TLS.
type fakeIssuer struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeIssuer{key: key}
	mux := http.NewServeMux()
	f.srv = httptest.NewTLSServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "jwks_uri": f.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "ci", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) token(t *testing.T, aud, sub string, extra map[string]any) string {
	now := time.Now()
	c := jwt.MapClaims{"iss": f.srv.URL, "aud": aud, "sub": sub, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"jti": uuid.NewString(), "repository_owner_id": "4242"}
	for k, v := range extra {
		c[k] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = "ci"
	s, err := tok.SignedString(f.key)
	require.NoError(t, err)
	return s
}

func TestE2E_Federation(t *testing.T) {
	d := db(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: d}, 0)
	ring, err := sts.NewKeyRing(d, kek)
	require.NoError(t, err)
	require.NoError(t, ring.Rotate(ctx))
	svc := sts.NewService(issuer, ring, d)
	ci := newFakeIssuer(t)
	svc.Federation.Client = ci.srv.Client() // le vrai client refuse 127.0.0.1 (testé plus bas)
	sas, err := iam.NewServiceAccountService(d, engine, []byte("integration-test-pepper-0123456789abcdef"))
	require.NoError(t, err)
	policies := &iam.PolicyService{DB: d, Engine: engine}

	awa := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	fatou := iam.UserPrincipal(uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	projectID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	project := iam.ProjectName(projectID)
	sa, err := sas.Create(ctx, awa, projectID, "ci-"+uuid.NewString()[:8], "CI", "")
	require.NoError(t, err)
	view, _ := policies.Get(ctx, project)
	next := view.Policy
	next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: "roles/sites.deployer", Members: []iam.Principal{sa.Principal}})
	_, err = policies.Set(ctx, awa, project, next)
	require.NoError(t, err)

	// ── Relations de confiance : règles de création ──
	_, err = sas.CreateTrust(ctx, fatou, projectID, sa.ID, iam.FederationTrustInput{Issuer: ci.srv.URL, SubjectPattern: "repo:x/y"})
	assert.ErrorIs(t, err, iam.ErrActAsRequired)
	_, err = sas.CreateTrust(ctx, awa, projectID, sa.ID, iam.FederationTrustInput{Issuer: "http://insecure", SubjectPattern: "repo:x/y"})
	assert.ErrorIs(t, err, iam.ErrInvalidIssuer)
	_, err = sas.CreateTrust(ctx, awa, projectID, sa.ID, iam.FederationTrustInput{Issuer: iam.IssuerGitHubActions, SubjectPattern: "repo:*"})
	assert.ErrorIs(t, err, iam.ErrSubjectTooBroad, "tout GitHub")
	_, err = sas.CreateTrust(ctx, awa, projectID, sa.ID, iam.FederationTrustInput{Issuer: iam.IssuerGitLab, SubjectPattern: "project_path:*"})
	assert.ErrorIs(t, err, iam.ErrSubjectTooBroad, "tout gitlab.com")
	_, err = sas.CreateTrust(ctx, awa, projectID, sa.ID, iam.FederationTrustInput{Issuer: ci.srv.URL, SubjectPattern: "re*po:x"})
	assert.ErrorIs(t, err, iam.ErrInvalidSubjectPattern)
	_, err = sas.CreateTrust(ctx, awa, projectID, sa.ID, iam.FederationTrustInput{Issuer: iam.IssuerGitHubActions, SubjectPattern: "repo:sunupay/api:*"})
	require.NoError(t, err, "propriétaire fixé : accepté")

	trust, err := sas.CreateTrust(ctx, awa, projectID, sa.ID, iam.FederationTrustInput{DisplayName: "GitHub main",
		Issuer: ci.srv.URL + "/", SubjectPattern: "repo:sunupay/api:ref:refs/heads/main",
		Conditions: map[string]string{"repository_owner_id": "4242"}})
	require.NoError(t, err)
	assert.Equal(t, ci.srv.URL, trust.Issuer, "émetteur normalisé")

	// ── HTTP ──
	gin.SetMode(gin.TestMode)
	mw := security.NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil)
	mw.SetAuthorizer(engine)
	mw.SetTokenVerifier(svc)
	r := gin.New()
	r.POST("/iam/token", (&iamapi.Controller{STS: svc}).Token)
	api := r.Group("/", mw.AuthMiddleware())
	site := uuid.NewString()
	api.POST("/p/:id/sites/:siteID/deploy", mw.Require(iam.SiteDeploymentsCreate, security.ProjectChild("id", "sites", "siteID")),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	exchange := func(subjectToken string, extra url.Values) (int, map[string]any) {
		form := url.Values{"grant_type": {sts.GrantTokenExchange}, "subject_token": {subjectToken},
			"subject_token_type": {sts.TokenTypeJWT}}
		for k, v := range extra {
			form[k] = v
		}
		req := httptest.NewRequest("POST", "/iam/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	deploy := func(token string) int {
		req := httptest.NewRequest("POST", "/p/"+projectID.String()+"/sites/"+site+"/deploy", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	mainSub := "repo:sunupay/api:ref:refs/heads/main"

	// Échange réussi : jeton de 15 min qui porte les droits du compte.
	ciToken := ci.token(t, svc.Issuer, mainSub, nil)
	code, body := exchange(ciToken, nil)
	require.Equal(t, http.StatusOK, code, body)
	assert.InDelta(t, 900, body["expires_in"], 5)
	assert.Equal(t, sts.TokenTypeAccess, body["issued_token_type"])
	access := body["access_token"].(string)
	assert.Equal(t, http.StatusOK, deploy(access))

	// Refus.
	for name, tc := range map[string]struct {
		token string
		want  string
	}{
		"rejeu":                       {ciToken, "replay"},
		"autre branche":               {ci.token(t, svc.Issuer, "repo:sunupay/api:ref:refs/heads/dev", nil), "no federation trust matches"},
		"autre dépôt":                 {ci.token(t, svc.Issuer, "repo:pirate/api:ref:refs/heads/main", nil), "no federation trust matches"},
		"propriétaire renommé":        {ci.token(t, svc.Issuer, mainSub, map[string]any{"repository_owner_id": "9999"}), "no federation trust matches"},
		"audience d'un autre service": {ci.token(t, "https://autre.example", mainSub, nil), "audience"},
	} {
		code, body := exchange(tc.token, nil)
		assert.Equal(t, http.StatusBadRequest, code, name)
		assert.Equal(t, "invalid_grant", body["error"], name)
		assert.Contains(t, body["error_description"], tc.want, name)
	}
	// Émetteur inconnu : aucune requête vers lui.
	forged := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://evil.example", "aud": svc.Issuer,
		"sub": mainSub, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()})
	forgedStr, _ := forged.SignedString(ci.key)
	code, body = exchange(forgedStr, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error_description"], "no federation trust for this issuer")
	code, body = exchange(ci.token(t, svc.Issuer, mainSub, nil), url.Values{"subject_token_type": {"urn:x"}})
	assert.Equal(t, "invalid_request", body["error"])

	// Deux comptes font confiance au même sujet : il faut préciser lequel.
	sa2, err := sas.Create(ctx, awa, projectID, "ci2-"+uuid.NewString()[:8], "", "")
	require.NoError(t, err)
	_, err = sas.CreateTrust(ctx, awa, projectID, sa2.ID, iam.FederationTrustInput{Issuer: ci.srv.URL, SubjectPattern: "repo:sunupay/*"})
	require.NoError(t, err)
	code, body = exchange(ci.token(t, svc.Issuer, mainSub, nil), nil)
	assert.Equal(t, "invalid_request", body["error"])
	assert.Contains(t, body["error_description"], "service_account")
	code, _ = exchange(ci.token(t, svc.Issuer, mainSub, nil), url.Values{"service_account": {string(sa2.Principal)}})
	assert.Equal(t, http.StatusOK, code)

	// Suppression de la relation : le jeton émis tombe.
	require.NoError(t, sas.DeleteTrust(ctx, projectID, sa.ID, trust.ID))
	svc.ForgetStatus()
	assert.Equal(t, http.StatusUnauthorized, deploy(access))

	list, err := sas.ListTrusts(ctx, projectID, sa.ID)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

// Le client de fédération refuse de contacter une adresse interne (SSRF).
func TestFederationClient_RefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, err := sts.NewIssuers().Client.Get(srv.URL) // 127.0.0.1
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private address")
}

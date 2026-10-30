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

const k8sIssuer = "https://kubernetes.default.svc.cluster.local"

func k8sJWKS(t *testing.T, key *rsa.PrivateKey, kid string) []byte {
	raw, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}})
	require.NoError(t, err)
	return raw
}

func podToken(t *testing.T, key *rsa.PrivateKey, kid, aud, sub string) string {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": k8sIssuer, "aud": []string{aud}, "sub": sub,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Hour).Unix()})
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	require.NoError(t, err)
	return s
}

// Identité des workloads : le pod échange son jeton Kubernetes projeté contre
// un jeton du compte de service du workload, sans aucune clé.
func TestE2E_WorkloadIdentity(t *testing.T) {
	d := db(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: d}, 0)
	ring, err := sts.NewKeyRing(d, kek)
	require.NoError(t, err)
	require.NoError(t, ring.Rotate(ctx))
	svc := sts.NewService(issuer, ring, d)
	cluster := "k3s-wl-" + uuid.NewString()[:6]
	svc.WorkloadClusterID = cluster
	sas, err := iam.NewServiceAccountService(d, engine, []byte("integration-test-pepper-0123456789abcdef"))
	require.NoError(t, err)
	agents := iam.NewClusterAgents(sas)
	policies := &iam.PolicyService{DB: d, Engine: engine}

	awa := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	fatou := iam.UserPrincipal(uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	projectID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	sa, err := sas.Create(ctx, awa, projectID, "api-"+uuid.NewString()[:8], "API", "")
	require.NoError(t, err)
	view, _ := policies.Get(ctx, iam.ProjectName(projectID))
	next := view.Policy
	next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: "roles/sites.deployer", Members: []iam.Principal{sa.Principal}})
	_, err = policies.Set(ctx, awa, iam.ProjectName(projectID), next)
	require.NoError(t, err)

	// ── « Exécuter comme » : actAs exigé, compte du même projet ──
	_, err = sas.AuthorizeRunAs(ctx, fatou, projectID, string(sa.Principal))
	assert.ErrorIs(t, err, iam.ErrActAsRequired)
	_, err = sas.AuthorizeRunAs(ctx, awa, uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"), string(sa.Principal))
	assert.ErrorIs(t, err, iam.ErrServiceAccountNotFound, "compte d'un autre projet")
	byAccountID, err := sas.AuthorizeRunAs(ctx, awa, projectID, sa.AccountID)
	require.NoError(t, err, "désigné par son identifiant de compte")
	saID, err := sas.AuthorizeRunAs(ctx, awa, projectID, string(sa.Principal))
	require.NoError(t, err)
	assert.Equal(t, saID, byAccountID)

	// Workload qui tourne comme ce compte.
	ns, name, workloadID := "km-sunupay", "api-"+uuid.NewString()[:6], uuid.New()
	require.NoError(t, d.Exec(`INSERT INTO workloads (id, project_id, name, namespace, chart_name, version, image, service_account_id)
		VALUES (?, ?, ?, ?, 'standard-app', '1.0.0', 'nginx:1', ?)`, workloadID, projectID, name, ns, saID).Error)

	// ── Le watcher publie les clés de son cluster ──
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	assert.ErrorIs(t, agents.ReportServiceAccountKeys(ctx, cluster, k8sIssuer, []byte(`{"keys":[]}`)), iam.ErrInvalidClusterJWKS)
	require.NoError(t, agents.ReportServiceAccountKeys(ctx, cluster, k8sIssuer, k8sJWKS(t, key, "k1")))

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
	exchange := func(subject string) (int, map[string]any) {
		form := url.Values{"grant_type": {sts.GrantTokenExchange}, "subject_token": {subject}, "subject_token_type": {sts.TokenTypeJWT}}
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
	sub := "system:serviceaccount:" + ns + ":" + name

	pod := podToken(t, key, "k1", svc.Issuer, sub)
	code, body := exchange(pod)
	require.Equal(t, http.StatusOK, code, body)
	access := body["access_token"].(string)
	assert.Equal(t, http.StatusOK, deploy(access))
	code, _ = exchange(pod)
	assert.Equal(t, http.StatusOK, code, "le kubelet réutilise le même jeton : pas d'usage unique")

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for label, tok := range map[string]string{
		"autre namespace":        podToken(t, key, "k1", svc.Issuer, "system:serviceaccount:km-pirate:"+name),
		"pas un compte k8s":      podToken(t, key, "k1", svc.Issuer, "user:admin"),
		"clé d'un autre cluster": podToken(t, other, "k1", svc.Issuer, sub),
		"audience par défaut":    podToken(t, key, "k1", "https://kubernetes.default.svc", sub),
	} {
		code, body := exchange(tok)
		assert.Equal(t, http.StatusBadRequest, code, label)
		assert.Equal(t, "invalid_grant", body["error"], label)
	}

	// Rotation des clés du cluster : les nouvelles sont prises en compte.
	key2, _ := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, agents.ReportServiceAccountKeys(ctx, cluster, k8sIssuer, k8sJWKS(t, key2, "k2")))
	code, _ = exchange(podToken(t, key2, "k2", svc.Issuer, sub))
	assert.Equal(t, http.StatusOK, code)

	// Le workload ne tourne plus comme le compte : ses jetons tombent.
	require.NoError(t, d.Exec(`UPDATE workloads SET service_account_id = NULL WHERE id = ?`, workloadID).Error)
	svc.ForgetStatus()
	assert.Equal(t, http.StatusUnauthorized, deploy(access))
	code, _ = exchange(podToken(t, key2, "k2", svc.Issuer, sub))
	assert.Equal(t, http.StatusBadRequest, code)
}

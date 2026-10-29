package iam_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

var pepper = []byte("integration-test-pepper-0123456789abcdef")

// De bout en bout, en HTTP : compte de service → rôle → clé → appel
// authentifié par la clé → restrictions → révocation → suppression.
func TestE2E_ServiceAccountsAndKeys(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, iam.DefaultCacheTTL)
	policies := &iam.PolicyService{DB: db, Engine: engine}
	sas, err := iam.NewServiceAccountService(db, engine, pepper)
	require.NoError(t, err)

	awa := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	fatou := iam.UserPrincipal(uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	projectID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	project := iam.ProjectName(projectID)

	// ── Compte de service ──
	_, err = sas.Create(ctx, awa, projectID, "CI", "", "")
	assert.ErrorIs(t, err, iam.ErrInvalidAccountID)
	accountID := "ci-" + uuid.NewString()[:8]
	sa, err := sas.Create(ctx, awa, projectID, accountID, "CI GitHub", "déploie les sites")
	require.NoError(t, err)
	_, err = sas.Create(ctx, awa, projectID, accountID, "", "")
	assert.ErrorIs(t, err, iam.ErrAccountIDTaken)

	// Rôle accordé par setIamPolicy (membre serviceAccount:<uuid> résolu).
	view, err := policies.Get(ctx, project)
	require.NoError(t, err)
	next := view.Policy
	next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: "roles/sites.deployer", Members: []iam.Principal{sa.Principal}})
	_, err = policies.Set(ctx, awa, project, next)
	require.NoError(t, err)

	// ── Clé : actAs exigé ──
	_, err = sas.CreateKey(ctx, fatou, projectID, sa.ID, iam.CreateKeyRequest{})
	assert.ErrorIs(t, err, iam.ErrActAsRequired, "sans actAs, pas de clé (sinon escalade)")
	_, err = sas.CreateKey(ctx, awa, projectID, sa.ID, iam.CreateKeyRequest{ExpiresInDays: 365})
	assert.ErrorIs(t, err, iam.ErrInvalidKeyExpiry)

	site := project.Child("sites", uuid.New())
	created, err := sas.CreateKey(ctx, awa, projectID, sa.ID, iam.CreateKeyRequest{
		DisplayName:  "github-actions",
		Restrictions: iam.KeyRestrictions{Resources: []iam.ResourceName{site}},
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(created.Key, "btr_live_"))
	assert.WithinDuration(t, time.Now().Add(90*24*time.Hour), created.ExpiresAt, time.Minute, "90 jours par défaut")
	var stored string
	require.NoError(t, db.Raw(`SELECT secret_hmac FROM api_keys_v2 WHERE key_id = ?`, created.KeyID).Row().Scan(&stored))
	assert.NotContains(t, created.Key, stored, "le secret n'est jamais stocké en clair")

	// ── Appels HTTP avec la clé ──
	gin.SetMode(gin.TestMode)
	mw := security.NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil)
	mw.SetAuthorizer(engine)
	mw.SetKeyAuthenticator(sas)
	r := gin.New()
	r.Use(mw.AuthMiddleware())
	ok := func(c *gin.Context) { p, _ := security.PrincipalFromContext(c); c.String(http.StatusOK, string(p)) }
	r.POST("/p/:id/sites/:siteID/deploy", mw.Require(iam.SiteDeploymentsCreate, security.ProjectChild("id", "sites", "siteID")), ok)
	r.DELETE("/p/:id/sites/:siteID", mw.Require(iam.SitesDelete, security.ProjectChild("id", "sites", "siteID")), ok)

	call := func(method, path, key, header string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if header == "bearer" {
			req.Header.Set("Authorization", "Bearer "+key)
		} else {
			req.Header.Set("X-API-Key", key)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	siteID := strings.TrimPrefix(string(site), string(project)+"/sites/")
	deploy := "/p/" + projectID.String() + "/sites/" + siteID + "/deploy"

	rec := call(http.MethodPost, deploy, created.Key, "x-api-key")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, string(sa.Principal), rec.Body.String(), "le principal est le compte de service")
	assert.Equal(t, http.StatusOK, call(http.MethodPost, deploy, created.Key, "bearer").Code, "aussi en Bearer")

	assert.Equal(t, http.StatusForbidden, call(http.MethodDelete, "/p/"+projectID.String()+"/sites/"+siteID, created.Key, "").Code,
		"le rôle du compte ne permet pas de supprimer")
	otherSite := "/p/" + projectID.String() + "/sites/" + uuid.NewString() + "/deploy"
	rec = call(http.MethodPost, otherSite, created.Key, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "restriction : un seul site")
	assert.Contains(t, rec.Body.String(), "restrictions")

	typo := created.Key[:len(created.Key)-1] + "x"
	assert.Equal(t, http.StatusUnauthorized, call(http.MethodPost, deploy, typo, "").Code)

	// Mauvais pepper : la base seule ne suffit pas à valider une clé.
	otherPepper, _ := iam.NewServiceAccountService(db, engine, []byte(strings.Repeat("z", 40)))
	_, err = otherPepper.Authenticate(ctx, created.Key, "203.0.113.1")
	assert.ErrorIs(t, err, iam.ErrAPIKeyInvalid)

	// Restriction IP.
	ipKey, err := sas.CreateKey(ctx, awa, projectID, sa.ID, iam.CreateKeyRequest{
		Restrictions: iam.KeyRestrictions{AllowedCIDRs: []string{"41.82.0.0/16"}}})
	require.NoError(t, err)
	_, err = sas.Authenticate(ctx, ipKey.Key, "102.16.0.1")
	assert.ErrorIs(t, err, iam.ErrAPIKeyInvalid)
	_, err = sas.Authenticate(ctx, ipKey.Key, "41.82.3.4")
	assert.NoError(t, err)

	// ── Révocation : effet immédiat ──
	require.NoError(t, sas.RevokeKey(ctx, projectID, sa.ID, created.KeyID))
	assert.Equal(t, http.StatusUnauthorized, call(http.MethodPost, deploy, created.Key, "").Code)
	assert.ErrorIs(t, sas.RevokeKey(ctx, projectID, sa.ID, created.KeyID), iam.ErrAPIKeyNotFound)

	keys, err := sas.ListKeys(ctx, projectID, sa.ID)
	require.NoError(t, err)
	assert.Len(t, keys, 2)
	assert.NotNil(t, keys[1].RevokedAt)
	assert.Equal(t, []iam.ResourceName{site}, keys[1].Restrictions.Resources)

	// ── Suppression du compte : clés et rôles disparaissent ──
	require.NoError(t, sas.Delete(ctx, awa, projectID, sa.ID))
	_, err = sas.Authenticate(ctx, ipKey.Key, "41.82.3.4")
	assert.ErrorIs(t, err, iam.ErrAPIKeyInvalid)
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM iam_bindings WHERE principal = ?`, string(sa.Principal)).Row().Scan(&n))
	assert.Zero(t, n, "plus aucun binding pour le compte supprimé")
	_, err = sas.Get(ctx, projectID, sa.ID)
	assert.ErrorIs(t, err, iam.ErrServiceAccountNotFound)
}

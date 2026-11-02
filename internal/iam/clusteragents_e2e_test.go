package iam_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

// Clés de cluster = comptes de service système (iam.md §7.4) : la clé d'un
// cluster ne vaut que pour ce cluster et que pour les routes du watcher.
func TestE2E_ClusterAgents(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, iam.DefaultCacheTTL)
	sas, err := iam.NewServiceAccountService(db, engine, pepper)
	require.NoError(t, err)
	agents := iam.NewClusterAgents(sas)
	staff := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))

	gin.SetMode(gin.TestMode)
	mw := security.NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil)
	mw.SetAuthorizer(engine)
	mw.SetKeyAuthenticator(sas)
	mw.SetClusterAgents(agents)
	r := gin.New()
	r.Use(mw.AuthMiddleware())
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r.POST("/enroll", mw.RequireClusterAgent(security.ClusterJSONBody("cluster_id")), ok)
	r.POST("/events/:clusterID", mw.RequireClusterAgent(security.ClusterParam("clusterID")), ok)
	r.GET("/admin", mw.Require(iam.PlatformClustersList, security.PlatformResource), ok)
	call := func(method, path, key, body string) int {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	// ── Création : compte système créé une fois, rôle accordé ──
	_, err = agents.CreateKey(ctx, staff, iam.CreateClusterKeyRequest{ClusterID: "bad cluster"})
	assert.ErrorIs(t, err, iam.ErrInvalidClusterID)
	cluster := "k3s-" + uuid.NewString()[:8]
	other := "k3s-" + uuid.NewString()[:8]
	k1, err := agents.CreateKey(ctx, staff, iam.CreateClusterKeyRequest{ClusterID: cluster})
	require.NoError(t, err)
	k2, err := agents.CreateKey(ctx, staff, iam.CreateClusterKeyRequest{ClusterID: cluster, ExpiresInDays: 10})
	require.NoError(t, err)
	assert.Equal(t, k1.ServiceAccountID, k2.ServiceAccountID, "un seul compte système par cluster")
	assert.Equal(t, "active", k1.Status)
	kOther, err := agents.CreateKey(ctx, staff, iam.CreateClusterKeyRequest{ClusterID: other})
	require.NoError(t, err)

	got, err := agents.ClusterOf(ctx, k1.Principal)
	require.NoError(t, err)
	assert.Equal(t, cluster, got)
	got, err = agents.ClusterOf(ctx, staff)
	require.NoError(t, err)
	assert.Empty(t, got, "un humain n'est l'agent d'aucun cluster")

	// Les comptes système n'apparaissent dans aucun projet.
	list, err := sas.List(ctx, uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))
	require.NoError(t, err)
	for _, sa := range list {
		assert.NotEqual(t, k1.ServiceAccountID, sa.ID)
	}

	// ── Watcher : son cluster seulement ──
	assert.Equal(t, http.StatusOK, call("POST", "/events/"+cluster, k1.Key, "{}"))
	// En-tête utilisé par le watcher.
	req := httptest.NewRequest("POST", "/events/"+cluster, bytes.NewBufferString("{}"))
	req.Header.Set("X-API-Key", k1.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, http.StatusOK, call("POST", "/enroll", k1.Key, `{"cluster_id":"`+cluster+`"}`))
	assert.Equal(t, http.StatusForbidden, call("POST", "/events/"+other, k1.Key, "{}"), "clé d'un autre cluster")
	assert.Equal(t, http.StatusForbidden, call("POST", "/enroll", k1.Key, `{"cluster_id":"`+other+`"}`))
	assert.Equal(t, http.StatusOK, call("POST", "/events/"+other, kOther.Key, "{}"))
	assert.Equal(t, http.StatusBadRequest, call("POST", "/enroll", k1.Key, `{}`))
	// Et rien d'autre : ni les routes staff, …
	assert.Equal(t, http.StatusForbidden, call("GET", "/admin", k1.Key, ""))

	// … ni une clé de projet, même avec des droits.
	projectID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	sa, err := sas.Create(ctx, staff, projectID, "ci-"+uuid.NewString()[:8], "", "")
	require.NoError(t, err)
	pk, err := sas.CreateKey(ctx, staff, projectID, sa.ID, iam.CreateKeyRequest{})
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, call("POST", "/events/"+cluster, pk.Key, "{}"))

	// ── Liste ──
	all, err := agents.List(ctx)
	require.NoError(t, err)
	ids := map[string]iam.ClusterKey{}
	for _, k := range all {
		ids[k.KeyID] = k
	}
	assert.Contains(t, ids, k1.KeyID)
	assert.Equal(t, "expiring", ids[k2.KeyID].Status, "10 jours : sous le seuil de 14")
	legacy, found := ids["km_prod_cccccccccccc"]
	require.True(t, found, "clé legacy migrée listée")
	assert.Equal(t, "k3s-dakar", legacy.ClusterID)
	assert.Equal(t, "legacy", legacy.Format)
	assert.NotEqual(t, uuid.Nil, legacy.ServiceAccountID, "rattachée au compte système (000008)")
	assert.Equal(t, "revoked", ids["km_prod_eeeeeeeeeeee"].Status, "clé de cluster sans cluster_id révoquée")

	// ── Rotation : l'ancienne reste valide pendant la grâce ──
	_, err = agents.Rotate(ctx, staff, k1.KeyID, iam.RotateClusterKeyRequest{GraceDays: 31})
	assert.ErrorIs(t, err, iam.ErrInvalidGraceDays)
	rotated, err := agents.Rotate(ctx, staff, k1.KeyID, iam.RotateClusterKeyRequest{})
	require.NoError(t, err)
	assert.Equal(t, k1.ServiceAccountID, rotated.ServiceAccountID)
	assert.Equal(t, http.StatusOK, call("POST", "/events/"+cluster, k1.Key, "{}"), "période de grâce")
	assert.Equal(t, http.StatusOK, call("POST", "/events/"+cluster, rotated.Key, "{}"))

	// Rotation immédiate (clé compromise).
	_, err = agents.Rotate(ctx, staff, k2.KeyID, iam.RotateClusterKeyRequest{Immediate: true})
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, call("POST", "/events/"+cluster, k2.Key, "{}"))
	_, err = agents.Rotate(ctx, staff, k2.KeyID, iam.RotateClusterKeyRequest{})
	assert.ErrorIs(t, err, iam.ErrClusterKeyInactive)

	// Rotation d'une clé legacy : nouvelle clé v2 sur le même compte système.
	fromLegacy, err := agents.Rotate(ctx, staff, "km_prod_cccccccccccc", iam.RotateClusterKeyRequest{GraceDays: 3})
	require.NoError(t, err)
	assert.Equal(t, "k3s-dakar", fromLegacy.ClusterID)
	assert.Equal(t, legacy.ServiceAccountID, fromLegacy.ServiceAccountID)
	assert.Equal(t, http.StatusOK, call("POST", "/events/k3s-dakar", fromLegacy.Key, "{}"))

	// ── Révocation ──
	require.NoError(t, agents.Revoke(ctx, rotated.KeyID))
	assert.Equal(t, http.StatusUnauthorized, call("POST", "/events/"+cluster, rotated.Key, "{}"))
	assert.ErrorIs(t, agents.Revoke(ctx, "nope"), iam.ErrClusterKeyNotFound)
	assert.ErrorIs(t, agents.Revoke(ctx, pk.KeyID), iam.ErrClusterKeyNotFound, "une clé de projet n'est pas une clé de cluster")

	// La réconciliation des rôles staff ne touche pas au rôle des agents.
	_, err = iam.ReconcilePlatformRoles(ctx, db, []iam.PlatformRoleAssignment{{Role: iam.RoleClusterAgent}}, "test")
	assert.ErrorIs(t, err, iam.ErrInvalidBinding)
}

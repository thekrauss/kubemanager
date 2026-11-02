package iam_test

import (
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
	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	authrepo "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	projectrepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
)

// Clés km_… reprises par la migration 000007 (données du script de test) :
// elles agissent comme leur compte de service legacy, sur leur seul projet.
func TestE2E_LegacyKeysMigrated(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, iam.DefaultCacheTTL)
	sas, err := iam.NewServiceAccountService(db, engine, pepper)
	require.NoError(t, err)

	awa := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	sunupay := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	awaKey := "km_prod_aaaaaaaaaaaa_secretawa"       // owner de sunupay
	moussaKey := "km_prod_bbbbbbbbbbbb_secretmoussa" // developer de sunupay

	// Un autre projet d'Awa : sa clé legacy n'y a plus accès.
	other := &authdomain.Project{Name: "awa-other-" + uuid.NewString()[:6]}
	require.NoError(t, projectrepo.NewProjectRepository(db).CreateProject(ctx, other, awa.String()))

	gin.SetMode(gin.TestMode)
	mw := security.NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(),
		authrepo.NewAuthRepository(db), nil, nil, nil, nil, nil)
	mw.SetAuthorizer(engine)
	mw.SetKeyAuthenticator(sas)
	r := gin.New()
	r.Use(mw.AuthMiddleware())
	who := func(c *gin.Context) {
		p, _ := security.PrincipalFromContext(c)
		c.String(http.StatusOK, string(p)+"|"+c.GetString("userID"))
	}
	r.POST("/p/:id/workloads", mw.Require(iam.WorkloadsCreate, security.ProjectParam("id")), who)
	r.DELETE("/p/:id", mw.Require(iam.ProjectsDelete, security.ProjectParam("id")), who)

	call := func(method, path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-API-Key", key)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := call(http.MethodDelete, "/p/"+sunupay.String(), awaKey)
	require.Equal(t, http.StatusOK, rec.Code, "clé d'owner : droits d'owner conservés sur son projet")
	assert.Regexp(t, `^serviceAccount:[0-9a-f-]{36}\|$`, rec.Body.String(),
		"la clé agit comme le compte de service, sans userID dans le contexte")

	assert.Equal(t, http.StatusForbidden, call(http.MethodDelete, "/p/"+other.ID.String(), awaKey).Code,
		"isolation : la clé ne vaut plus que pour son projet")

	assert.Equal(t, http.StatusOK, call(http.MethodPost, "/p/"+sunupay.String()+"/workloads", moussaKey).Code)
	assert.Equal(t, http.StatusForbidden, call(http.MethodDelete, "/p/"+sunupay.String(), moussaKey).Code,
		"clé de developer : parité exacte, pas de suppression")

	// Supprimer le compte legacy révoque ses clés km_… aussi.
	var saID uuid.UUID
	require.NoError(t, db.Raw(`SELECT service_account_id FROM api_keys WHERE prefix = 'km_prod_bbbbbbbbbbbb'`).Row().Scan(&saID))
	require.NoError(t, sas.Delete(ctx, iam.UserPrincipal(awa), sunupay, saID))
	assert.Equal(t, http.StatusUnauthorized, call(http.MethodPost, "/p/"+sunupay.String()+"/workloads", moussaKey).Code)
}

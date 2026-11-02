package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/loopfz/gadgeto/tonic"
	"github.com/stretchr/testify/assert"
	"github.com/wI2L/fizz"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

type accessIn struct {
	Provider string `path:"provider"`
}

// Une route Public() est exemptée d'authentification par son chemin gin
// (paramètres compris) ; ses voisines Authenticated() restent protégées.
func TestAccess_PublicRoutesReachAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := security.NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil)
	engine := gin.New()
	engine.Use(mw.AuthMiddleware())
	f := fizz.NewFromEngine(engine)

	h := tonic.Handler(func(c *gin.Context, in *accessIn) (string, error) { return "ok", nil }, http.StatusOK)
	root := &RouteGroup{Path: PathAPIRoot}
	group := root.NewGroup("/zz-access-test", "test")
	group.AddRoute("/social/:provider", http.MethodGet, "public", Public(), h)
	group.AddRoute("/me", http.MethodGet, "auth", Authenticated(), h)
	registerGroup(f.Group(root.Path, "", ""), root, root.Path, false)
	mw.SetPublicRoutes(PublicRoutes())

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PathAPIRoot+"/zz-access-test/social/google", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "route publique accessible sans jeton")

	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PathAPIRoot+"/zz-access-test/me", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "route authentifiée refusée sans jeton")
}

func TestAccess_MissingAccessPanicsAtStartup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := fizz.NewFromEngine(gin.New())
	g := &RouteGroup{Path: "/zz-missing"}
	g.AddRoute("/x", http.MethodGet, "x", Access{}, func(c *gin.Context) {})
	assert.Panics(t, func() { registerGroup(f.Group(g.Path, "", ""), g, g.Path, false) })
}

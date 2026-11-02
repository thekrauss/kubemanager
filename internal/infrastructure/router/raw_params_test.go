package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	buildsDomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	sitesDomain "github.com/thekrauss/kubemanager/internal/modules/sites/domain"
)

// Paramètres des handlers bruts (envois, flux) : lus avec les étiquettes path:.
// Une étiquette uri: restante laissait le champ vide : « ProjectID : champ
// obligatoire » sur tout envoi de site, invisible avec une API simulée.
func TestWrapRawHandler_ReadsPathParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var site sitesDomain.UploadPath
	var build buildsDomain.UploadPath
	engine.POST("/projects/:id/sites/:siteID/deployments", wrapRawHandler(func(c *gin.Context, in *sitesDomain.UploadPath) {
		site = *in
		c.Status(http.StatusNoContent)
	}))
	engine.POST("/projects/:id/builds/upload", wrapRawHandler(func(c *gin.Context, in *buildsDomain.UploadPath) {
		build = *in
		c.Status(http.StatusNoContent)
	}))

	for path, check := range map[string]func(){
		"/projects/p1/sites/s1/deployments": func() {
			assert.Equal(t, "p1", site.ProjectID)
			assert.Equal(t, "s1", site.SiteID)
		},
		"/projects/p1/builds/upload": func() { assert.Equal(t, "p1", build.ProjectID) },
	} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusNoContent, rec.Code, path+" : "+rec.Body.String())
		check()
	}
}

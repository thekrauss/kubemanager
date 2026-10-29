package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func serveLocalized(lang, cookie string, h gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Locale(), TranslateMessages())
	r.GET("/x", h)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "btr_locale", Value: cookie})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func denied(c *gin.Context) {
	c.JSON(http.StatusForbidden, gin.H{"code": "PERMISSION_DENIED", "error": "permission denied", "message": "permission denied"})
}

func TestTranslateMessages_ByLocale(t *testing.T) {
	w := serveLocalized("fr-SN,fr;q=0.9", "", denied)
	assert.JSONEq(t, `{"code":"PERMISSION_DENIED","error":"Permission refusée","message":"Permission refusée"}`, w.Body.String())
	assert.Equal(t, "fr", w.Header().Get("Content-Language"))
	assert.Contains(t, w.Header().Values("Vary"), "Accept-Language")

	w = serveLocalized("en-GB", "", denied)
	assert.Contains(t, w.Body.String(), `"permission denied"`)
	assert.Equal(t, "en", w.Header().Get("Content-Language"))
}

func TestTranslateMessages_CookieWinsOverBrowserLanguage(t *testing.T) {
	w := serveLocalized("en-US", "fr", denied)
	assert.Contains(t, w.Body.String(), "Permission refusée", "la langue choisie dans la console prime")
}

func TestTranslateMessages_DefaultsToFrenchAndLeavesDataAlone(t *testing.T) {
	w := serveLocalized("", "", denied)
	assert.Contains(t, w.Body.String(), "Permission refusée")

	w = serveLocalized("fr", "", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"name": "permission denied", "items": []string{"site introuvable"}})
	})
	assert.JSONEq(t, `{"name":"permission denied","items":["site introuvable"]}`, w.Body.String(),
		"les données d'une réponse réussie ne sont jamais modifiées")
}

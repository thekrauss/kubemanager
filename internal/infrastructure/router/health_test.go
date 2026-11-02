package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/core/configs"
)

func TestRunReadiness_AllOK(t *testing.T) {
	ok := func(context.Context) error { return nil }
	status, body := runReadiness(context.Background(), []readinessCheck{{"database", ok}, {"redis", ok}}, nil)
	if status != http.StatusOK || body["status"] != readinessStatusReady {
		t.Fatalf("status = %d, body = %v", status, body)
	}
}

// Une dépendance en panne : 503, sans divulguer le message d'erreur.
func TestRunReadiness_OneDown(t *testing.T) {
	ok := func(context.Context) error { return nil }
	down := func(context.Context) error { return errors.New("dial tcp 172.30.0.2:5432 secret") }
	status, body := runReadiness(context.Background(), []readinessCheck{{"database", down}, {"redis", ok}}, nil)
	if status != http.StatusServiceUnavailable || body["status"] != readinessStatusNotReady {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	checks := body["checks"].(map[string]string)
	if checks["database"] != readinessCheckDown || checks["redis"] != readinessCheckOK {
		t.Fatalf("checks = %v", checks)
	}
}

// Un contrôle qui ne répond pas ne bloque pas la sonde au-delà du budget.
func TestRunReadiness_Timeout(t *testing.T) {
	hang := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	start := time.Now()
	status, _ := runReadiness(context.Background(), []readinessCheck{{"temporal", hang}}, nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", status)
	}
	if elapsed := time.Since(start); elapsed > readinessTimeout+time.Second {
		t.Fatalf("durée = %v", elapsed)
	}
}

func TestLimitRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const limit = 16
	engine := gin.New()
	engine.Use(limitRequestBody(limit))
	var form string
	handler := func(c *gin.Context) {
		form = c.PostForm("device_code")
		c.Status(http.StatusOK)
	}
	engine.POST("/kmanager/v1/identity/device/token", handler)
	engine.POST("/kmanager/v1/projects/:id/sites/:siteID/deployments", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	post := func(path, body string, chunked bool) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if chunked {
			req.ContentLength = -1 // longueur inconnue : seul MaxBytesReader protège
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec.Code
	}

	big := "device_code=" + strings.Repeat("x", 100)
	if code := post("/kmanager/v1/identity/device/token", "device_code=ok", false); code != http.StatusOK || form != "ok" {
		t.Fatalf("petit corps : code %d, form %q", code, form)
	}
	if code := post("/kmanager/v1/identity/device/token", big, false); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("corps annoncé trop gros : code %d", code)
	}
	form = "inchangé"
	post("/kmanager/v1/identity/device/token", big, true)
	if form != "" {
		t.Fatalf("corps sans longueur annoncée lu malgré le plafond : %q", form)
	}
	if code := post("/kmanager/v1/projects/1/sites/2/deployments", big, false); code != http.StatusOK {
		t.Fatalf("route à limite propre : code %d", code)
	}
}

func TestSwaggerConfigured(t *testing.T) {
	cases := []struct {
		cfg  configs.SwaggerConfig
		want bool
	}{
		{configs.SwaggerConfig{}, false},
		{configs.SwaggerConfig{User: "u"}, false},
		{configs.SwaggerConfig{Password: "p"}, false},
		{configs.SwaggerConfig{User: " ", Password: "p"}, false},
		{configs.SwaggerConfig{User: "u", Password: "p"}, true},
	}
	for _, c := range cases {
		if got := swaggerConfigured(c.cfg); got != c.want {
			t.Errorf("%+v : %v", c.cfg, got)
		}
	}
}

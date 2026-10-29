package edge

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
)

type memObject struct{ *bytes.Reader }

func (memObject) Close() error { return nil }

type memStore struct{ files map[string]string }

func (m *memStore) Get(_ context.Context, key string) (objectstore.Object, objectstore.ObjectInfo, error) {
	body, ok := m.files[key]
	if !ok {
		return nil, objectstore.ObjectInfo{}, objectstore.ErrNotFound
	}
	ct := "application/octet-stream"
	if strings.HasSuffix(key, ".html") {
		ct = "text/html; charset=utf-8"
	}
	return memObject{bytes.NewReader([]byte(body))}, objectstore.ObjectInfo{
		Size: int64(len(body)), ETag: "abc", ContentType: ct, LastModified: time.Unix(0, 0),
	}, nil
}
func (m *memStore) Put(context.Context, string, io.Reader, int64, string) error { return nil }
func (m *memStore) Download(context.Context, string, io.Writer) error           { return nil }
func (m *memStore) Delete(context.Context, string) error                        { return nil }
func (m *memStore) DeletePrefix(context.Context, string) error                  { return nil }

type mapResolver map[string]*Site

func (m mapResolver) Resolve(_ context.Context, host string) (*Site, error) { return m[host], nil }

const pfx = "sites/s1/d1/"

func newTestServer(spa bool, files map[string]string) *Server {
	return &Server{
		Resolver: mapResolver{"shop.example.com": {ID: "s1", DeploymentID: "d1", SPAFallback: spa}},
		Store:    &memStore{files: files},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func get(h http.Handler, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServe_IndexAndFiles(t *testing.T) {
	h := newTestServer(false, map[string]string{
		pfx + "index.html":      "home",
		pfx + "app.js":          "js",
		pfx + "docs/index.html": "docs",
		pfx + "about.html":      "about",
	}).Handler()

	rec := get(h, "shop.example.com:443", "/")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "home", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Cache-Control"), "max-age=0")

	rec = get(h, "SHOP.example.com", "/app.js")
	assert.Equal(t, "js", rec.Body.String())
	assert.Equal(t, "public, max-age=3600", rec.Header().Get("Cache-Control"))

	rec = get(h, "shop.example.com", "/docs")
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/docs/", rec.Header().Get("Location"))

	assert.Equal(t, "docs", get(h, "shop.example.com", "/docs/").Body.String())
	assert.Equal(t, "about", get(h, "shop.example.com", "/about").Body.String())
}

func TestServe_PathTraversalStaysInsideSite(t *testing.T) {
	h := newTestServer(false, map[string]string{pfx + "index.html": "home", "sites/other/d/secret.html": "x"}).Handler()
	rec := get(h, "shop.example.com", "/../../other/d/secret.html")
	assert.NotEqual(t, "x", rec.Body.String())
}

func TestServe_NotFoundAndSPA(t *testing.T) {
	files := map[string]string{pfx + "index.html": "app", pfx + "404.html": "custom 404"}

	rec := get(newTestServer(false, files).Handler(), "shop.example.com", "/missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "custom 404", rec.Body.String())

	rec = get(newTestServer(true, files).Handler(), "shop.example.com", "/dashboard/settings")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "app", rec.Body.String())

	rec = get(newTestServer(false, files).Handler(), "unknown.example.com", "/")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_RejectsWriteMethods(t *testing.T) {
	h := newTestServer(false, map[string]string{pfx + "index.html": "home"}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "shop.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestCheckDomain(t *testing.T) {
	h := newTestServer(false, nil).InternalHandler()
	assert.Equal(t, http.StatusOK, get(h, "edge", "/check?domain=shop.example.com").Code)
	assert.Equal(t, http.StatusNotFound, get(h, "edge", "/check?domain=evil.example.com").Code)
	assert.Equal(t, http.StatusBadRequest, get(h, "edge", "/check").Code)
}

type countingResolver struct {
	calls int
	site  *Site
}

func (c *countingResolver) Resolve(context.Context, string) (*Site, error) {
	c.calls++
	return c.site, nil
}

func TestCachedResolver_CachesIncludingMisses(t *testing.T) {
	next := &countingResolver{}
	c := NewCachedResolver(next, time.Minute, 0)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		s, err := c.Resolve(context.Background(), "x.example.com")
		require.NoError(t, err)
		assert.Nil(t, s)
	}
	assert.Equal(t, 1, next.calls)

	now = now.Add(2 * time.Minute)
	_, _ = c.Resolve(context.Background(), "x.example.com")
	assert.Equal(t, 2, next.calls, "entrée expirée → nouvelle résolution")
}

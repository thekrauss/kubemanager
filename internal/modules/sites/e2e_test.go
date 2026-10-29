// Test d'intégration : vrai Postgres + vrai S3 (SeaweedFS), ignoré sans SITES_E2E.
//
//	docker run -d --rm --name sites-e2e-pg -e POSTGRES_PASSWORD=pw -p 127.0.0.1:55432:5432 postgres:15-alpine
//	docker run -d --rm --name sites-e2e-s3 -p 127.0.0.1:58333:8333 \
//	  -v $PWD/docker/seaweedfs/s3.json:/etc/seaweedfs/s3.json:ro chrislusf/seaweedfs:4.50 \
//	  server -dir=/data -s3 -s3.port=8333 -s3.config=/etc/seaweedfs/s3.json
//	for f in migrations/*.up.sql; do docker exec -i sites-e2e-pg psql -U postgres < $f; done
//	SITES_E2E=1 go test ./internal/modules/sites/ -run TestE2E -v
package sites_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/edge"
	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	"github.com/thekrauss/kubemanager/internal/modules/sites/activities"
	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
	"github.com/thekrauss/kubemanager/internal/modules/sites/repository"
)

const dsn = "postgres://postgres:pw@127.0.0.1:55432/postgres?sslmode=disable"

func s3cfg(user, secret string) objectstore.Config {
	return objectstore.Config{Endpoint: "127.0.0.1:58333", AccessKey: user, SecretKey: secret,
		Bucket: "betorun-sites", Region: "us-east-1"}
}

func TestE2E_SiteDeployAndServe(t *testing.T) {
	if os.Getenv("SITES_E2E") == "" {
		t.Skip("SITES_E2E not set")
	}
	ctx := context.Background()

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	projectID := uuid.New()
	require.NoError(t, gdb.Exec("INSERT INTO projects (id, name) VALUES (?, ?)", projectID, "e2e-"+projectID.String()[:8]).Error)

	platform, err := objectstore.New(s3cfg("betorun", "betorun-dev-secret"))
	require.NoError(t, err)
	require.NoError(t, objectstore.EnsureBucket(ctx, platform, "us-east-1"))

	repo := repository.NewSiteRepository(gdb)
	site := &domain.Site{ID: uuid.New(), ProjectID: projectID, Name: "shop", Subdomain: "shop-" + projectID.String()[:6], Status: domain.SiteStatusEmpty}
	require.NoError(t, repo.CreateSite(ctx, site))
	dep := &domain.SiteDeployment{ID: uuid.New(), SiteID: site.ID, Status: domain.DeploymentStatusPending}
	dep.ArchiveKey = domain.ArchiveKey(site.ID, dep.ID)
	require.NoError(t, repo.CreateDeployment(ctx, dep))

	// Archive « dist/ » comme la sort un build Vite.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"dist/index.html": "<h1>Boutique</h1>", "dist/assets/app.css": "body{color:red}", "dist/404.html": "perdu",
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	require.NoError(t, zw.Close())
	require.NoError(t, platform.Put(ctx, dep.ArchiveKey, bytes.NewReader(buf.Bytes()), int64(buf.Len()), "application/zip"))

	acts := &activities.SiteActivities{Repo: repo, Store: platform, Logger: zap.NewNop().Sugar()}
	ref := activities.DeploymentRef{SiteID: site.ID.String(), DeploymentID: dep.ID.String()}
	res, err := acts.ExtractDeployment(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, 3, res.FileCount)
	require.NoError(t, acts.MarkDeploymentReady(ctx, activities.ReadyInput{DeploymentRef: ref, FileCount: res.FileCount, SizeBytes: res.SizeBytes}))
	require.NoError(t, acts.DeleteArchive(ctx, ref))

	// ── Edge : identifiants S3 lecture seule + DB via database/sql ──
	edgeStore, err := objectstore.New(s3cfg("edge", "edge-dev-secret"))
	require.NoError(t, err)
	err = edgeStore.Put(ctx, "sites/hack.html", strings.NewReader("x"), 1, "text/html")
	assert.Error(t, err, "l'edge ne doit pas pouvoir écrire")

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	newHandler := func() (http.Handler, http.Handler) {
		srv := &edge.Server{
			Resolver: edge.NewCachedResolver(&edge.SQLResolver{DB: db, BaseDomain: "sites.localhost"}, time.Minute, 0),
			Store:    edgeStore,
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		return srv.Handler(), srv.InternalHandler()
	}
	h, internal := newHandler()
	host := site.Subdomain + ".sites.localhost:8088"

	rec := do(h, host, "/")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>Boutique</h1>", rec.Body.String())
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))

	rec = do(h, host, "/assets/app.css")
	assert.Equal(t, "body{color:red}", rec.Body.String())
	assert.Equal(t, "text/css; charset=utf-8", rec.Header().Get("Content-Type"))

	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	req.Host = host
	req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	assert.Equal(t, http.StatusNotModified, rec2.Code, "ETag S3 → 304")

	rec = do(h, host, "/introuvable")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "perdu", rec.Body.String())

	// ── Domaine personnalisé : servi seulement une fois vérifié ──
	d := &domain.SiteDomain{ID: uuid.New(), SiteID: site.ID, Hostname: "www." + site.Subdomain + ".example"}
	require.NoError(t, repo.CreateDomain(ctx, d))
	h, internal = newHandler()
	assert.Equal(t, http.StatusNotFound, do(h, d.Hostname, "/").Code)
	assert.Equal(t, http.StatusNotFound, do(internal, "edge", "/check?domain="+d.Hostname).Code)

	require.NoError(t, repo.SetDomainVerification(ctx, d.ID, true, time.Now()))
	h, internal = newHandler()
	assert.Equal(t, "<h1>Boutique</h1>", do(h, d.Hostname, "/").Body.String())
	assert.Equal(t, http.StatusOK, do(internal, "edge", "/check?domain="+d.Hostname).Code)

	// ── Suppression : plus servi, plus de fichiers ──
	require.NoError(t, repo.SetSiteStatus(ctx, site.ID, domain.SiteStatusDeleting))
	h, _ = newHandler()
	assert.Equal(t, http.StatusNotFound, do(h, host, "/").Code, "DELETING n'est plus servi")
	require.NoError(t, acts.DeleteSiteObjects(ctx, site.ID.String()))
	require.NoError(t, acts.DeleteSiteRecord(ctx, site.ID.String()))
	_, _, err = platform.Get(ctx, domain.ObjectPrefix(site.ID, dep.ID)+"index.html")
	assert.ErrorIs(t, err, objectstore.ErrNotFound)
}

func do(h http.Handler, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

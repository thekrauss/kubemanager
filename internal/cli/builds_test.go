package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// API minimale : un build qui réussit (ou échoue) et un conteneur.
func fakeBuildAPI(t *testing.T, finalStatus, failure string) (*httptest.Server, *atomic.Value, *atomic.Value) {
	t.Helper()
	id := uuid.New()
	image := "reg/" + projectID + "/api:b-1"
	body := func(status string) map[string]any {
		return map[string]any{"id": id, "name": "api", "status": status, "failure": failure, "image": image}
	}
	var fields, deployed atomic.Value
	mux := http.NewServeMux()
	base := "/projects/" + projectID + "/builds"
	mux.HandleFunc("POST "+base+"/upload", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		fh := r.MultipartForm.File["archive"]
		require.Len(t, fh, 1)
		fields.Store(r.FormValue("name") + "|" + r.FormValue("dockerfile"))
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(body("QUEUED"))
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		fields.Store(string(raw))
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(body("QUEUED"))
	})
	mux.HandleFunc("GET "+base+"/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "#1 journal du build\n")
	})
	mux.HandleFunc("GET "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(body(finalStatus))
	})
	mux.HandleFunc("POST /workloads", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		deployed.Store(req["image"])
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"workload_id": uuid.NewString(), "status": "STARTING"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &fields, &deployed
}

func buildEnv(t *testing.T, apiURL string) string {
	t.Helper()
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_TOKEN", "ci-token")
	t.Setenv("BETORUN_API_URL", apiURL)
	t.Setenv("BETORUN_PROJECT", projectID)
	buildPollEvery = time.Millisecond
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM x"), 0o644))
	return dir
}

// --source : build, journal affiché, puis déploiement de l'image obtenue.
func TestWorkloadsDeploy_FromSource(t *testing.T) {
	srv, fields, deployed := fakeBuildAPI(t, "SUCCEEDED", "")
	dir := buildEnv(t, srv.URL)
	out, err := run(t, "workloads", "deploy", "-n", "api", "--source", dir, "--no-wait")
	require.NoError(t, err, out)
	assert.Equal(t, "api|Dockerfile", fields.Load())
	assert.Contains(t, out, "journal du build")
	assert.Contains(t, out, "Image construite")
	assert.Equal(t, "reg/"+projectID+"/api:b-1", deployed.Load())
}

// Build en échec : rien n'est déployé.
func TestWorkloadsDeploy_BuildFailureStops(t *testing.T) {
	srv, _, deployed := fakeBuildAPI(t, "FAILED", "BUILD_FAILED")
	dir := buildEnv(t, srv.URL)
	_, err := run(t, "workloads", "deploy", "-n", "api", "--source", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "le build a échoué")
	assert.Nil(t, deployed.Load())
}

func TestBuildsCreate_Git(t *testing.T) {
	srv, fields, _ := fakeBuildAPI(t, "SUCCEEDED", "")
	buildEnv(t, srv.URL)
	_, err := run(t, "builds", "create", "-n", "api", "--git", "https://github.com/a/b", "--ref", "v1", "--context-dir", "svc")
	require.NoError(t, err)
	got := fields.Load().(string)
	for _, want := range []string{`"git_url":"https://github.com/a/b"`, `"git_ref":"v1"`, `"context_dir":"svc"`} {
		assert.Contains(t, got, want)
	}
}

// Erreurs de saisie, avant tout appel à l'API.
func TestBuilds_InputErrors(t *testing.T) {
	srv, _, _ := fakeBuildAPI(t, "SUCCEEDED", "")
	dir := buildEnv(t, srv.URL)

	_, err := run(t, "workloads", "deploy", "-n", "api", "--source", dir, "--image", "nginx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "une seule origine")

	_, err = run(t, "workloads", "deploy", "-n", "api")
	require.Error(t, err)

	// Un Dockerfile explicite doit exister ; sinon, détection au build.
	_, err = run(t, "builds", "create", "-n", "api", "--source", dir, "--dockerfile", "docker/absent")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "introuvable"), err.Error())
}

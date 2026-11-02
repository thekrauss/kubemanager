package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "betorun.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestProjectFile_Validation(t *testing.T) {
	t.Setenv("BETORUN_LANG", "fr")
	_, err := loadProjectFile(writeFile(t, "name: api\nimage: nginx\nreplicass: 2\n"))
	require.Error(t, err, "faute de frappe : erreur")
	assert.Contains(t, err.Error(), "replicass")

	// Pas d'URL d'API : un dépôt cloné ne détourne pas les identifiants.
	_, err = loadProjectFile(writeFile(t, "name: api\nimage: nginx\napi: https://evil.example\n"))
	require.Error(t, err)

	_, err = loadProjectFile(writeFile(t, "image: nginx\n"))
	require.Error(t, err, "nom requis")
	_, err = loadProjectFile(writeFile(t, "name: api\n"))
	require.Error(t, err, "origine requise")
	_, err = loadProjectFile(writeFile(t, "name: api\nimage: nginx\nbuild: {context: .}\n"))
	require.Error(t, err, "une seule origine")
	_, err = loadProjectFile(filepath.Join(t.TempDir(), "absent.yaml"))
	require.Error(t, err)
}

func TestProjectFile_EnvReferences(t *testing.T) {
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("DB_URL", "postgres://x")
	pf, err := loadProjectFile(writeFile(t, "name: api\nimage: nginx\nenv:\n  URL: ${DB_URL}\n  LOG: info\n  TXT: a$b\n"))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"URL": "postgres://x", "LOG": "info", "TXT": "a$b"}, pf.Env)

	_, err = loadProjectFile(writeFile(t, "name: api\nimage: nginx\nenv:\n  URL: ${ABSENTE_XYZ}\n"))
	require.Error(t, err, "variable absente : pas de déploiement avec une valeur vide")
	assert.Contains(t, err.Error(), "ABSENTE_XYZ")
}

func TestDefaultName(t *testing.T) {
	assert.Equal(t, "mon-app", defaultName("Mon App"))
	assert.Equal(t, "api-v2", defaultName("_api.v2_"))
	assert.Equal(t, "app", defaultName("___"))
}

// Crée si le conteneur n'existe pas, met à jour s'il existe, et le contexte
// du build est relatif au fichier, pas au dossier courant.
func TestDeploy_CreateThenUpdate(t *testing.T) {
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_TOKEN", "ci-token")
	t.Setenv("BETORUN_PROJECT", "")
	buildPollEvery = time.Millisecond

	wid := uuid.New()
	var existing []map[string]any
	var created, updated map[string]any
	var uploadProject string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /workloads", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(existing) })
	mux.HandleFunc("POST /workloads", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&created)
		_ = json.NewEncoder(w).Encode(map[string]any{"workload_id": wid.String()})
	})
	mux.HandleFunc("PATCH /workloads/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&updated)
		w.WriteHeader(http.StatusAccepted)
	})
	bid := uuid.New()
	build := map[string]any{"id": bid, "name": "api", "status": "SUCCEEDED", "image": "reg/p/api:b-1"}
	mux.HandleFunc("POST /projects/{p}/builds/upload", func(w http.ResponseWriter, r *http.Request) {
		uploadProject = r.PathValue("p")
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(build)
	})
	mux.HandleFunc("GET /projects/{p}/builds/{id}/logs", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /projects/{p}/builds/{id}", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(build) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("BETORUN_API_URL", srv.URL)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "svc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "svc", "Dockerfile"), []byte("FROM x"), 0o644))
	file := filepath.Join(dir, "betorun.yaml")
	require.NoError(t, os.WriteFile(file, []byte("name: api\nproject: "+projectID+"\nbuild:\n  context: svc\nport: 3000\nreplicas: 2\ncpu: 300m\ntls: true\nenv:\n  A: b\n"), 0o644))

	_, err := run(t, "deploy", "-f", file, "--no-wait")
	require.NoError(t, err)
	assert.Equal(t, projectID, uploadProject, "projet du fichier")
	assert.Equal(t, "reg/p/api:b-1", created["image"])
	assert.EqualValues(t, 3000, created["target_port"])
	assert.EqualValues(t, 2, created["replicas"])
	assert.Equal(t, "300m", created["cpu_limit"])
	assert.Equal(t, true, created["tls_enabled"])
	assert.Nil(t, updated)

	existing = []map[string]any{{"ID": wid.String(), "Name": "api"}}
	created = nil
	_, err = run(t, "deploy", "-f", file, "--no-wait")
	require.NoError(t, err)
	assert.Nil(t, created, "conteneur existant : mise à jour")
	assert.Equal(t, "reg/p/api:b-1", updated["image"])
	assert.EqualValues(t, 2, updated["replicas"])
	assert.EqualValues(t, 3000, updated["target_port"])
	assert.Equal(t, map[string]any{"A": "b"}, updated["env_vars"])
}

func TestInit(t *testing.T) {
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("Dockerfile", []byte("FROM x\nEXPOSE 3000\n"), 0o644))
	_, err := run(t, "init", "-n", "api")
	require.NoError(t, err)
	pf, err := loadProjectFile("betorun.yaml")
	require.NoError(t, err)
	assert.Equal(t, "api", pf.Name)
	assert.Equal(t, 3000, pf.Port)
	require.NotNil(t, pf.Build)

	_, err = run(t, "init")
	require.Error(t, err, "ne remplace pas un fichier existant")
}

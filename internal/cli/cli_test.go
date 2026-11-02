package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cliapi "github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/config"
)

// fakeAPI : routes de l'API utilisées par la CLI.
type fakeAPI struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	polls    atomic.Int32
	refresh  atomic.Int32
	sites    []map[string]any
	uploaded map[string]bool // fichiers reçus dans l'archive
	depPolls atomic.Int32
	failNext string // "step_up" | "forbidden"
	logout   atomic.Int32
}

const (
	projectID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	validTok  = "tok-2"
)

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		h := r.Header.Get("Authorization")
		if h != "Bearer "+validTok && h != "Bearer ci-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid or expired token"}`))
			return false
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch f.failNext {
		case "step_up":
			f.failNext = ""
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"STEP_UP_REQUIRED","message":"recent two-factor authentication required"}`))
			return false
		case "forbidden":
			f.failNext = ""
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"PERMISSION_DENIED","message":"permission refusée"}`))
			return false
		}
		return true
	}
	mux.HandleFunc("POST /kmanager/v1/identity/device/code", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"ABCD-EFGH","verification_uri":"https://console.betorun.app/device",
			"verification_uri_complete":"https://console.betorun.app/device?user_code=ABCD-EFGH","expires_in":60,"interval":0}`))
	})
	mux.HandleFunc("POST /kmanager/v1/identity/device/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "dc", body["device_code"])
		if f.polls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		// Jeton déjà expiré : la commande suivante doit le renouveler.
		_, _ = w.Write([]byte(`{"access_token":"tok-1","refresh_token":"rt-1","expires_in":1}`))
	})
	mux.HandleFunc("POST /kmanager/v1/identity/refresh", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["refresh_token"] != "rt-1" && body["refresh_token"] != "rt-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.refresh.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"` + validTok + `","refresh_token":"rt-2","expires_in":300}`))
	})
	mux.HandleFunc("POST /kmanager/v1/identity/logout", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			f.logout.Add(1)
		}
	})
	mux.HandleFunc("GET /kmanager/v1/identity/bootstrap/status", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_, _ = w.Write([]byte(`{"bootstrapped":true,"email":"awa@ex.sn","project_id":"` + projectID +
				`","project":{"id":"` + projectID + `","name":"sunupay"}}`))
		}
	})
	mux.HandleFunc("GET /kmanager/v1/iam/session", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_, _ = w.Write([]byte(`{"principal":"user:11111111-1111-1111-1111-111111111111","auth_method":"bearer","mfa_available":true,"mfa":true}`))
		}
	})
	sitesPath := "/kmanager/v1/projects/" + projectID + "/sites"
	mux.HandleFunc("GET "+sitesPath, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			f.mu.Lock()
			defer f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(append([]map[string]any{}, f.sites...))
		}
	})
	mux.HandleFunc("POST "+sitesPath, func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.NotContains(t, body, "ProjectID", "le projet est dans l'URL")
		site := map[string]any{"id": uuid.NewString(), "name": body["name"], "status": "EMPTY",
			"spa_fallback": body["spa_fallback"], "url": "https://" + body["name"].(string) + ".betorun.app"}
		f.mu.Lock()
		f.sites = append(f.sites, site)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(site)
	})
	depID := uuid.NewString()
	mux.HandleFunc("GET "+sitesPath+"/{site}", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, s := range f.sites {
				if s["id"] == r.PathValue("site") {
					s["status"] = "LIVE"
					_ = json.NewEncoder(w).Encode(s)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("POST "+sitesPath+"/{site}/deployments", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		file, _, err := r.FormFile("archive")
		require.NoError(t, err)
		raw, _ := io.ReadAll(file)
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		require.NoError(t, err)
		f.uploaded = map[string]bool{}
		for _, zf := range zr.File {
			f.uploaded[zf.Name] = true
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"` + depID + `","status":"PENDING"}`))
	})
	mux.HandleFunc("GET "+sitesPath+"/{site}/deployments", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			status := "PROCESSING"
			if f.depPolls.Add(1) >= 2 {
				status = "READY"
			}
			_, _ = w.Write([]byte(`[{"id":"` + depID + `","status":"` + status + `","file_count":2}]`))
		}
	})
	return f
}

func (f *fakeAPI) url() string { return f.srv.URL + "/kmanager/v1" }

// run : exécute la CLI comme un utilisateur ; sortie et erreur capturées.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot("betorun")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	flagAPI, flagProject, flagJSON = "", "", false
	err := root.Execute()
	return out.String(), err
}

func TestCLI_EndToEnd(t *testing.T) {
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_TOKEN", "")
	deployPollEvery = 10 * time.Millisecond
	cliapi.DefaultDeviceInterval = 10 * time.Millisecond
	api := newFakeAPI(t)

	// Sans API connue ni session.
	_, err := run(t, "whoami")
	require.Error(t, err)
	assert.Contains(t, describe(err), "URL de l'API inconnue")

	// ── login : code affiché, attente, session enregistrée en 0600 ──
	out, err := run(t, "login", "--device", "--api", api.url(), "--no-browser")
	require.NoError(t, err, out)
	assert.Contains(t, out, "ABCD-EFGH")
	assert.Contains(t, out, "https://console.betorun.app/device")
	assert.Contains(t, out, "Connecté en tant que awa@ex.sn")
	assert.Contains(t, out, "sunupay ("+projectID+")")
	dir, _ := config.Dir()
	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "session lisible par le seul utilisateur")

	// ── whoami : jeton expiré renouvelé automatiquement ──
	out, err = run(t, "whoami")
	require.NoError(t, err, out)
	assert.Equal(t, int32(1), api.refresh.Load())
	assert.Contains(t, out, "awa@ex.sn")
	assert.Contains(t, out, "Projet      "+projectID)
	assert.Contains(t, out, "Double auth.  oui")
	creds, _ := config.LoadCredentials()
	assert.Equal(t, validTok, creds.AccessToken, "session renouvelée et enregistrée")

	// ── sites deploy ──
	site := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(site, "index.html"), []byte("<h1>sunupay</h1>"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(site, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(site, "assets", "app.js"), []byte("console.log(1)"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(site, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(site, ".git", "config"), []byte("secret"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(site, ".DS_Store"), []byte("x"), 0o644))
	secret := filepath.Join(t.TempDir(), "id_rsa")
	require.NoError(t, os.WriteFile(secret, []byte("PRIVATE"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(site, "leak")))

	_, err = run(t, "sites", "deploy", site)
	assert.Contains(t, describe(err), "--site")
	_, err = run(t, "sites", "deploy", t.TempDir(), "--site", "vitrine", "--create")
	assert.Contains(t, describe(err), "aucun fichier à publier")
	_, err = run(t, "sites", "deploy", site, "--site", "vitrine")
	assert.Contains(t, describe(err), "--create")

	out, err = run(t, "sites", "deploy", site, "--site", "vitrine", "--create", "--spa")
	require.NoError(t, err, out)
	assert.Contains(t, out, "Création du site vitrine")
	assert.Contains(t, out, "2 fichiers")
	assert.Contains(t, out, "En ligne : https://vitrine.betorun.app")
	assert.Equal(t, map[string]bool{"index.html": true, "assets/app.js": true}, api.uploaded,
		"ni .git, ni fichiers parasites, ni lien symbolique suivi")

	out, err = run(t, "sites", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "vitrine")
	assert.Contains(t, out, "https://vitrine.betorun.app")

	// ── erreurs de l'API, dans la langue de l'utilisateur ──
	api.failNext = "step_up"
	_, err = run(t, "sites", "list")
	assert.Contains(t, describe(err), "confirmer votre identité")
	api.failNext = "forbidden"
	_, err = run(t, "sites", "list")
	assert.Contains(t, describe(err), "accès refusé : permission refusée")
	t.Setenv("BETORUN_LANG", "en")
	api.failNext = "step_up"
	_, err = run(t, "sites", "list")
	assert.Contains(t, describe(err), "confirm your identity")
	t.Setenv("BETORUN_LANG", "fr")

	// ── CI : jeton fourni, aucune session lue ──
	t.Setenv("BETORUN_TOKEN", "ci-token")
	_, err = run(t, "sites", "list")
	require.NoError(t, err)
	t.Setenv("BETORUN_TOKEN", "")

	// ── logout : session révoquée côté serveur puis effacée ──
	out, err = run(t, "logout")
	require.NoError(t, err)
	assert.Contains(t, out, "Déconnecté")
	assert.Equal(t, int32(1), api.logout.Load())
	_, err = run(t, "whoami")
	assert.Contains(t, describe(err), "betorun login")
	out, _ = run(t, "logout")
	assert.Contains(t, out, "Aucune session")
}

// La CLI est distribuée aux utilisateurs : aucune dépendance serveur.
func TestCLI_NoServerDependencies(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go absent")
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/thekrauss/kubemanager/cmd/betorun").CombinedOutput()
	require.NoError(t, err, string(out))
	for _, forbidden := range []string{"gorm.io/", "github.com/gin-gonic/", "helm.sh/", "k8s.io/", "go.temporal.io/",
		"github.com/redis/", "internal/iam", "internal/infrastructure", "internal/middleware"} {
		for _, line := range strings.Split(string(out), "\n") {
			assert.False(t, strings.Contains(line, forbidden), "dépendance serveur dans la CLI : %s", line)
		}
	}
}

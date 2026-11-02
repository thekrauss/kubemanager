package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/cli/config"
)

// fakeAuthAPI : API betorun (connexion de la CLI, step-up) en mémoire.
type fakeAuthAPI struct {
	mu        sync.Mutex
	challenge string // PKCE reçu à l'ouverture de la page d'autorisation
	redirect  string
	writes    atomic.Int32
	noFactor  bool
}

func newAuthAPI(t *testing.T) (*httptest.Server, *fakeAuthAPI) {
	f := &fakeAuthAPI{}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("GET /kmanager/v1/identity/cli/authorize-url", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Regexp(t, `^http://127\.0\.0\.1:\d+/callback$`, q.Get("redirect_uri"))
		assert.Regexp(t, `^[A-Za-z0-9_-]{43}$`, q.Get("code_challenge"), "PKCE S256 généré par la CLI")
		f.mu.Lock()
		f.challenge, f.redirect = q.Get("code_challenge"), q.Get("redirect_uri")
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://console.betorun.app/cli/authorize?" + q.Encode()})
	})
	mux.HandleFunc("POST /kmanager/v1/identity/cli/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		sum := sha256.Sum256([]byte(body["code_verifier"]))
		f.mu.Lock()
		ok := body["code"] == "C-1" && base64.RawURLEncoding.EncodeToString(sum[:]) == f.challenge && body["redirect_uri"] == f.redirect
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"code":"INVALID_INPUT","message":"this authorization has expired, run betorun login again"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"authenticated","access_token":"tok-cli","refresh_token":"rt-cli","expires_in":600}`))
	})
	mux.HandleFunc("POST /kmanager/v1/identity/session/verify", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case f.noFactor:
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"code":"MFA_NOT_ENROLLED","message":"two-factor authentication is not set up"}`))
		case body["code"] != "123456":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"code":"INVALID_CODE","message":"invalid verification code"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"authenticated","access_token":"tok-mfa","expires_in":600,"mfa":true}`))
		}
	})
	mux.HandleFunc("GET /kmanager/v1/identity/bootstrap/status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"bootstrapped":true,"email":"awa@ex.sn","project_id":"` + projectID + `"}`))
	})
	mux.HandleFunc("GET /kmanager/v1/iam/policy", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"policy":{"version":1,"etag":"e1","bindings":[]},"members":[]}`))
	})
	mux.HandleFunc("GET /kmanager/v1/iam/roles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"roles/viewer"}]`))
	})
	mux.HandleFunc("POST /kmanager/v1/iam/policy", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-mfa" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"code":"STEP_UP_REQUIRED","message":"recent two-factor authentication required for this action"}`))
			return
		}
		f.writes.Add(1)
		_, _ = w.Write([]byte(`{"policy":{"etag":"e2"}}`))
	})
	return srv, f
}

// browserFollows : faux navigateur — la personne autorise la CLI dans la
// console, qui redirige vers le serveur local avec le code et le state donnés
// (state vide : celui de la demande).
func browserFollows(t *testing.T, code, state string) func(string) {
	return func(link string) {
		u, err := url.Parse(link)
		require.NoError(t, err)
		q := u.Query()
		if state == "" {
			state = q.Get("state")
		}
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{"code": {code}, "state": {state}}.Encode())
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
}

func setupAuthCLI(t *testing.T, srv *httptest.Server) {
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_API_URL", srv.URL+"/kmanager/v1")
	t.Setenv("BETORUN_PROJECT", projectID)
	t.Setenv("BETORUN_TOKEN", "")
}

// Connexion par navigateur : PKCE de la CLI, state vérifié, session enregistrée.
func TestCLI_BrowserLogin(t *testing.T) {
	srv, _ := newAuthAPI(t)
	setupAuthCLI(t, srv)
	defer func(o func(string)) { openURL = o }(openURL)

	openURL = browserFollows(t, "C-1", "")
	out, err := run(t, "login")
	require.NoError(t, err, out)
	assert.Contains(t, out, "https://console.betorun.app/cli/authorize?")
	assert.Contains(t, out, "betorun login --device", "alternative sans navigateur indiquée")
	creds, _ := config.LoadCredentials()
	require.NotNil(t, creds)
	assert.Equal(t, "tok-cli", creds.AccessToken)
	assert.Equal(t, "rt-cli", creds.RefreshToken)

	// Un code injecté avec un autre state (autre onglet, autre application) est refusé.
	require.NoError(t, config.DeleteCredentials())
	openURL = browserFollows(t, "C-1", "AUTRE")
	_, err = run(t, "login")
	assert.Contains(t, describe(err), "state invalide")
	creds, _ = config.LoadCredentials()
	assert.Nil(t, creds)
}

// Action sensible : code demandé dans le terminal, nouvel essai après un code
// faux, puis la requête refusée est rejouée avec le jeton vérifié.
func TestCLI_StepUpInTerminal(t *testing.T) {
	srv, f := newAuthAPI(t)
	setupAuthCLI(t, srv)
	require.NoError(t, config.SaveCredentials(&config.Credentials{AccessToken: "tok-ancien", RefreshToken: "rt",
		ExpiresAt: time.Now().Add(time.Hour)}))
	defer func(r func(string) (string, error), s func() bool) { readCode, stepUpAllowed = r, s }(readCode, stepUpAllowed)
	stepUpAllowed = func() bool { return true }

	codes := []string{"000000", "123456"}
	readCode = func(string) (string, error) {
		c := codes[0]
		codes = codes[1:]
		return c, nil
	}
	out, err := run(t, "access", "grant", "moussa@ex.sn", "viewer")
	require.NoError(t, err, out)
	assert.Equal(t, int32(1), f.writes.Load(), "écriture rejouée une fois après la confirmation")
	creds, _ := config.LoadCredentials()
	assert.Equal(t, "tok-mfa", creds.AccessToken, "jeton vérifié enregistré")
	assert.Equal(t, "rt", creds.RefreshToken, "session conservée")

	// Aucun second facteur : message qui dit où l'activer.
	f.noFactor = true
	require.NoError(t, config.SaveCredentials(&config.Credentials{AccessToken: "tok-ancien", RefreshToken: "rt",
		ExpiresAt: time.Now().Add(time.Hour)}))
	readCode = func(string) (string, error) { return "123456", nil }
	_, err = run(t, "access", "grant", "moussa@ex.sn", "viewer")
	assert.Contains(t, describe(err), "Paramètres → Sécurité")

	// CI ou script : aucune saisie possible, message explicite.
	f.noFactor = false
	stepUpAllowed = func() bool { return false }
	_, err = run(t, "access", "grant", "moussa@ex.sn", "viewer")
	assert.Contains(t, describe(err), "relancez la commande dans un terminal")
}

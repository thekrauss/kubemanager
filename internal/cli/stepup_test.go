package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/cli/config"
)

// API qui exige un step-up pour écrire une policy, et faux Keycloak.
func newStepUpAPI(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var policyWrites atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("GET /kmanager/v1/identity/oidc/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "cli", q.Get("client"))
		assert.Equal(t, "true", q.Get("step_up"))
		assert.Regexp(t, `^http://127\.0\.0\.1:\d+/callback$`, q.Get("redirect_uri"))
		kc := "https://kc.example/auth?state=S-123&redirect_uri=" + url.QueryEscape(q.Get("redirect_uri"))
		_ = json.NewEncoder(w).Encode(map[string]string{"url": kc})
	})
	mux.HandleFunc("GET /kmanager/v1/identity/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code") != "C-1" || q.Get("state") != "S-123" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"message":"invalid state"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok-mfa","refresh_token":"rt-mfa","expires_in":300}`))
	})
	mux.HandleFunc("GET /kmanager/v1/iam/policy", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"policy":{"version":1,"etag":"e1","bindings":[]},"members":[]}`))
	})
	mux.HandleFunc("POST /kmanager/v1/iam/policy", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-mfa" {
			w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_user_authentication", acr_values="gold"`)
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"code":"STEP_UP_REQUIRED","message":"recent two-factor authentication required for this action"}`))
			return
		}
		policyWrites.Add(1)
		_, _ = w.Write([]byte(`{"policy":{"etag":"e2"}}`))
	})
	return srv, &policyWrites
}

// browserFollows : faux navigateur qui suit la redirection de Keycloak vers
// le serveur local de la CLI, avec le code et le state donnés.
func browserFollows(t *testing.T, code, state string) func(string) {
	return func(link string) {
		u, err := url.Parse(link)
		require.NoError(t, err)
		redirect := u.Query().Get("redirect_uri")
		go func() {
			resp, err := http.Get(redirect + "?code=" + code + "&state=" + state)
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
}

func TestCLI_StepUpThenRetry(t *testing.T) {
	srv, writes := newStepUpAPI(t)
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_API_URL", srv.URL+"/kmanager/v1")
	t.Setenv("BETORUN_PROJECT", projectID)
	t.Setenv("BETORUN_TOKEN", "")
	require.NoError(t, config.SaveCredentials(&config.Credentials{AccessToken: "tok-ancien", RefreshToken: "rt",
		ExpiresAt: time.Now().Add(time.Hour)}))
	defer func(o func(string), s func() bool) { openURL, stepUpAllowed = o, s }(openURL, stepUpAllowed)

	// Terminal interactif : confirmation dans le navigateur, puis la même
	// écriture rejouée avec la nouvelle session.
	stepUpAllowed = func() bool { return true }
	openURL = browserFollows(t, "C-1", "S-123")
	out, err := run(t, "access", "grant", "moussa@ex.sn", "viewer")
	require.NoError(t, err, out)
	assert.Equal(t, int32(1), writes.Load(), "écriture rejouée une fois après le step-up")
	creds, _ := config.LoadCredentials()
	assert.Equal(t, "tok-mfa", creds.AccessToken, "session MFA enregistrée")

	// Un code injecté avec un autre state est refusé.
	require.NoError(t, config.SaveCredentials(&config.Credentials{AccessToken: "tok-ancien", RefreshToken: "rt",
		ExpiresAt: time.Now().Add(time.Hour)}))
	openURL = browserFollows(t, "C-1", "AUTRE")
	_, err = run(t, "access", "grant", "moussa@ex.sn", "viewer")
	assert.Contains(t, describe(err), "state invalide")
	assert.Equal(t, int32(1), writes.Load())

	// CI ou script : pas de navigateur, message explicite.
	stepUpAllowed = func() bool { return false }
	_, err = run(t, "access", "grant", "moussa@ex.sn", "viewer")
	assert.Contains(t, describe(err), "betorun login --mfa")

	// login --mfa : même flux, à la demande.
	openURL = browserFollows(t, "C-1", "S-123")
	require.NoError(t, config.DeleteCredentials())
	srvLogin := srv.URL + "/kmanager/v1"
	_, _ = run(t, "login", "--mfa", "--api", srvLogin)
	creds, _ = config.LoadCredentials()
	require.NotNil(t, creds)
	assert.Equal(t, "tok-mfa", creds.AccessToken)
}

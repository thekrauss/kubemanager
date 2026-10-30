package services

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
)

type memStates map[string]string

func (m memStates) SetValue(_ context.Context, k, v string, _ time.Duration) error {
	m[k] = v
	return nil
}
func (m memStates) GetValue(_ context.Context, k string) (string, error) { return m[k], nil }
func (m memStates) DeleteValue(_ context.Context, k string) error        { delete(m, k); return nil }

// Connexion de la CLI (RFC 8252) : boucle locale uniquement, client de la
// CLI, mémorisé dans le state pour l'échange du code.
func TestLoginURL_CLI(t *testing.T) {
	kc, err := keycloakadmin.New(keycloakadmin.AdminConfig{BaseURL: "http://kc", Realm: "kubemanager",
		ClientID: "kubemanager-admin", ClientSecret: "s", WebClientID: "kubemanager-web"}, "")
	require.NoError(t, err)
	states := memStates{}
	s := NewService(Deps{Keycloak: kc, StateStore: states, CLIClientID: "betorun-cli", StepUpACR: "gold"}).(*service)
	ctx := context.Background()

	for _, bad := range []string{"https://evil.example/callback", "http://127.0.0.1:8080/autre",
		"http://localhost:8080/callback", "http://127.0.0.1.evil.example:80/callback"} {
		_, err := s.LoginURL(ctx, bad, LoginOptions{CLI: true})
		assert.ErrorIs(t, err, ErrCLIRedirectURI, bad)
	}

	raw, err := s.LoginURL(ctx, "http://127.0.0.1:54321/callback", LoginOptions{CLI: true, StepUp: true})
	require.NoError(t, err)
	u, _ := url.Parse(raw)
	q := u.Query()
	assert.Equal(t, "betorun-cli", q.Get("client_id"))
	assert.Equal(t, "gold", q.Get("acr_values"))
	assert.Equal(t, "0", q.Get("max_age"), "ré-authentification forcée")
	assert.Equal(t, "S256", q.Get("code_challenge_method"))

	var st oauthState
	require.NoError(t, json.Unmarshal([]byte(states["oauth:state:"+q.Get("state")]), &st))
	assert.Equal(t, "betorun-cli", st.ClientID, "le code s'échangera avec le client de la CLI")

	// Connexion web : client web, rien de mémorisé.
	raw, err = s.LoginURL(ctx, "http://localhost:3000/auth/callback", LoginOptions{})
	require.NoError(t, err)
	assert.True(t, strings.Contains(raw, "client_id=kubemanager-web"))

	// Sans client CLI configuré : refusé.
	off := NewService(Deps{Keycloak: kc, StateStore: memStates{}}).(*service)
	_, err = off.LoginURL(ctx, "http://127.0.0.1:54321/callback", LoginOptions{CLI: true})
	assert.ErrorIs(t, err, ErrDeviceLoginDisabled)
}

// Flux device avec PKCE : le verifier reste côté serveur et accompagne le sondage.
func TestDeviceLogin_PKCE(t *testing.T) {
	var challenge, verifier string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/auth/device") {
			challenge = r.PostForm.Get("code_challenge")
			_, _ = w.Write([]byte(`{"device_code":"dc-1","user_code":"AB-CD","verification_uri":"https://kc/device","expires_in":600,"interval":5}`))
			return
		}
		verifier = r.PostForm.Get("code_verifier")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":300}`))
	}))
	defer srv.Close()
	kc, err := keycloakadmin.New(keycloakadmin.AdminConfig{BaseURL: srv.URL, Realm: "kubemanager",
		ClientID: "kubemanager-admin", ClientSecret: "s"}, "")
	require.NoError(t, err)
	states := memStates{}
	s := NewService(Deps{Keycloak: kc, StateStore: states, CLIClientID: "betorun-cli"})
	ctx := context.Background()

	auth, err := s.StartDeviceLogin(ctx)
	require.NoError(t, err)
	assert.Equal(t, "AB-CD", auth.UserCode)
	assert.NotEmpty(t, challenge)
	for k := range states {
		assert.NotContains(t, k, "dc-1", "le device_code n'est pas stocké en clair")
	}

	_, err = s.PollDeviceLogin(ctx, "inconnu")
	var dte *keycloakadmin.DeviceTokenError
	require.ErrorAs(t, err, &dte)
	assert.Equal(t, "expired_token", dte.Code)

	tokens, err := s.PollDeviceLogin(ctx, "dc-1")
	require.NoError(t, err)
	assert.Equal(t, "at", tokens.AccessToken)
	sum := sha256.Sum256([]byte(verifier))
	assert.Equal(t, challenge, base64.RawURLEncoding.EncodeToString(sum[:]), "verifier du challenge envoyé")
	assert.Empty(t, states, "verifier effacé après usage")
}

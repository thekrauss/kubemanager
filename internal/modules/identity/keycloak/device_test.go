package keycloak

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Flux device contre un faux Keycloak : code, attente, puis jetons.
func TestDeviceFlow(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		assert.Equal(t, "betorun-cli", r.PostForm.Get("client_id"))
		assert.Empty(t, r.PostForm.Get("client_secret"), "client public : pas de secret")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/realms/kubemanager/protocol/openid-connect/auth/device":
			assert.Equal(t, "challenge", r.PostForm.Get("code_challenge"))
			assert.Equal(t, "S256", r.PostForm.Get("code_challenge_method"))
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://kc/device",
				"verification_uri_complete":"https://kc/device?user_code=WDJB-MJHT","expires_in":600,"interval":5}`))
		case "/realms/kubemanager/protocol/openid-connect/token":
			assert.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", r.PostForm.Get("grant_type"))
			assert.Equal(t, "dc", r.PostForm.Get("device_code"))
			assert.Equal(t, "verifier", r.PostForm.Get("code_verifier"))
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending","error_description":"The authorization request is still pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":300}`))
		default:
			t.Fatalf("chemin inattendu %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := New(AdminConfig{BaseURL: srv.URL, Realm: "kubemanager", ClientID: "kubemanager-admin", ClientSecret: "s3cret"}, "")
	require.NoError(t, err)
	ctx := context.Background()

	auth, err := c.StartDeviceAuthorization(ctx, "betorun-cli", "challenge")
	require.NoError(t, err)
	assert.Equal(t, "WDJB-MJHT", auth.UserCode)
	assert.Equal(t, 5, auth.Interval)
	u, _ := url.Parse(auth.VerificationURIComplete)
	assert.Equal(t, "WDJB-MJHT", u.Query().Get("user_code"))

	_, err = c.PollDeviceToken(ctx, "betorun-cli", "dc", "verifier")
	var pending *DeviceTokenError
	require.True(t, errors.As(err, &pending))
	assert.Equal(t, "authorization_pending", pending.Code)

	tokens, err := c.PollDeviceToken(ctx, "betorun-cli", "dc", "verifier")
	require.NoError(t, err)
	assert.Equal(t, "at", tokens.AccessToken)
	assert.Equal(t, "rt", tokens.RefreshToken)
}

// Le code s'échange avec le client qui l'a demandé (CLI : public, sans secret).
func TestExchangeCodeForToken_ClientOverride(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":300}`))
	}))
	defer srv.Close()
	c, err := New(AdminConfig{BaseURL: srv.URL, Realm: "kubemanager", ClientID: "kubemanager-admin",
		ClientSecret: "s3cret", WebClientID: "kubemanager-web"}, "")
	require.NoError(t, err)

	_, err = c.ExchangeCodeForToken(context.Background(), "code", "http://127.0.0.1:5/callback", "verifier", "betorun-cli")
	require.NoError(t, err)
	assert.Equal(t, "betorun-cli", got.Get("client_id"))
	assert.Empty(t, got.Get("client_secret"))

	_, err = c.ExchangeCodeForToken(context.Background(), "code", "http://app/cb", "verifier")
	require.NoError(t, err)
	assert.Equal(t, "kubemanager-web", got.Get("client_id"), "défaut : client web")
}

package social

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGoogle : jetons d'identité signés RS256, clés publiées en JWKS.
type fakeGoogle struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	claims jwt.MapClaims
	signer *rsa.PrivateKey // clé qui signe (≠ key : signature invalide)
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeGoogle{key: key, signer: key}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/certs":
			n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig", "n": n, "e": e}}})
		case "/token":
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "verifier-1", r.PostForm.Get("code_verifier"), "PKCE transmis")
			if r.PostForm.Get("code") != "good" {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			tok := jwt.NewWithClaims(jwt.SigningMethodRS256, f.claims)
			tok.Header["kid"] = "k1"
			signed, err := tok.SignedString(f.signer)
			require.NoError(t, err)
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": signed, "access_token": "at"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestGoogle(t *testing.T) {
	f := newFakeGoogle(t)
	g := NewGoogle(Config{ClientID: "betorun-app", ClientSecret: "s", TokenURL: f.srv.URL + "/token", JWKSURL: f.srv.URL + "/certs"})
	base := jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "betorun-app", "sub": "1089",
		"exp": time.Now().Add(time.Hour).Unix(), "email": "Awa@Gmail.com", "email_verified": true, "name": "Awa", "nonce": "n-1"}
	ctx := context.Background()

	u, _ := url.Parse(g.AuthCodeURL("st", "ch", "n-1", "https://console/auth/callback/google"))
	q := u.Query()
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, "n-1", q.Get("nonce"))
	assert.Equal(t, "openid email profile", q.Get("scope"))

	f.claims = base
	p, err := g.Exchange(ctx, "good", "verifier-1", "n-1", "cb")
	require.NoError(t, err)
	assert.Equal(t, Profile{Provider: Google, Subject: "1089", Email: "awa@gmail.com", EmailVerified: true, Name: "Awa"}, *p)

	refused := func(label string, claims jwt.MapClaims, nonce string) {
		f.claims = claims
		_, err := g.Exchange(ctx, "good", "verifier-1", nonce, "cb")
		assert.ErrorIs(t, err, ErrProviderRefused, label)
	}
	refused("nonce d'une autre demande (rejeu)", base, "n-2")
	other := maps.Clone(base)
	other["aud"] = "autre-app"
	refused("jeton émis pour une autre application", other, "n-1")
	other["aud"], other["iss"] = "betorun-app", "https://evil.example"
	refused("autre émetteur", other, "n-1")
	_, err = g.Exchange(ctx, "bad", "verifier-1", "n-1", "cb")
	assert.ErrorIs(t, err, ErrProviderRefused, "code refusé par Google")

	evil, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.signer, f.claims = evil, base
	_, err = g.Exchange(ctx, "good", "verifier-1", "n-1", "cb")
	assert.ErrorIs(t, err, ErrProviderRefused, "signature d'une autre clé")
}

func TestGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			require.NoError(t, r.ParseForm())
			assert.NotEmpty(t, r.PostForm.Get("code_verifier"))
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_x"})
		case "/user":
			assert.Equal(t, "Bearer gho_x", r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 4242, "login": "kofi", "name": "", "avatar_url": "https://a/k.png"})
		case "/user/emails":
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"email": "old@ex.gh", "primary": false, "verified": true},
				map[string]any{"email": "Kofi@ex.gh", "primary": true, "verified": true},
			})
		}
	}))
	defer srv.Close()
	h := NewGitHub(Config{ClientID: "gh-app", ClientSecret: "s", TokenURL: srv.URL + "/login/oauth/access_token", APIURL: srv.URL})
	p, err := h.Exchange(context.Background(), "code", "verifier", "", "cb")
	require.NoError(t, err)
	assert.Equal(t, Profile{Provider: GitHub, Subject: "4242", Email: "kofi@ex.gh", EmailVerified: true, Name: "kofi",
		AvatarURL: "https://a/k.png"}, *p, "adresse principale, nom de connexion à défaut de nom")
	assert.Contains(t, h.AuthCodeURL("st", "ch", "", "cb"), "scope=read%3Auser+user%3Aemail")
}

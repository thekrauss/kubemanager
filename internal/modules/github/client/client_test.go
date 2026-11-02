package client

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClient(t *testing.T, api string) (*Client, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	raw := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	c, err := New(Config{AppID: "42", Slug: "betorun", ClientID: "cid", ClientSecret: "cs", PrivateKeyPEM: raw, APIURL: api, WebURL: api})
	require.NoError(t, err)
	return c, key
}

func TestSignatureValid(t *testing.T) {
	body := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	assert.True(t, SignatureValid("s3cret", body, good))
	assert.False(t, SignatureValid("autre", body, good))
	assert.False(t, SignatureValid("s3cret", []byte(`{"a":2}`), good))
	assert.False(t, SignatureValid("s3cret", body, ""))
	assert.False(t, SignatureValid("s3cret", body, strings.TrimPrefix(good, "sha256=")))
	assert.False(t, SignatureValid("", body, good), "secret vide : jamais valide")
}

func TestAppJWT(t *testing.T) {
	c, key := testClient(t, "http://x")
	fixed := time.Now()
	c.now = func() time.Time { return fixed }
	raw, err := c.appJWT()
	require.NoError(t, err)
	claims := jwt.RegisteredClaims{}
	tok, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"RS256"}))
	require.NoError(t, err)
	require.True(t, tok.Valid)
	assert.Equal(t, "42", claims.Issuer)
	assert.WithinDuration(t, fixed.Add(-60*time.Second), claims.IssuedAt.Time, time.Second)
	assert.WithinDuration(t, fixed.Add(9*time.Minute), claims.ExpiresAt.Time, time.Second)
}

// Le jeton de clonage demande un seul dépôt et contents:read, rien d'autre.
func TestInstallationToken_CloneScope(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/app/installations/7/access_tokens", r.URL.Path)
		assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_x"}`))
	}))
	defer srv.Close()
	c, _ := testClient(t, srv.URL)
	token, err := c.InstallationToken(context.Background(), 7, CloneScope(99))
	require.NoError(t, err)
	assert.Equal(t, "ghs_x", token)
	assert.Equal(t, []any{float64(99)}, body["repository_ids"])
	assert.Equal(t, map[string]any{"contents": "read"}, body["permissions"])
}

// Une erreur ne porte jamais le jeton, seulement le chemin et le statut.
func TestErrors_NoSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"ghs_secret"}`))
	}))
	defer srv.Close()
	c, _ := testClient(t, srv.URL)
	_, err := c.RepositoryByID(context.Background(), "ghs_secret", 1)
	require.ErrorIs(t, err, ErrNotAccessible)
	assert.NotContains(t, err.Error(), "ghs_secret")
}

func TestUserInstallation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer ghu_x", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"installations":[{"id":5,"account":{"login":"a","type":"User"}}]}`))
	}))
	defer srv.Close()
	c, _ := testClient(t, srv.URL)
	found, err := c.UserInstallation(context.Background(), "ghu_x", 5)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "a", found.Account.Login)
	found, err = c.UserInstallation(context.Background(), "ghu_x", 6)
	require.NoError(t, err)
	assert.Nil(t, found, "installation d'un autre : pas de preuve")
}

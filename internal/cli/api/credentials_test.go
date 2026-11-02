package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fichier de clé : assertion ES256 signée avec la clé du fichier, vérifiable
// avec sa clé publique ; jeton mis en cache, redemandé après Invalidate.
func TestKeyFileSource(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)

	var calls atomic.Int32
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.NoError(t, r.ParseForm())
		assert.Equal(t, grantJWTBearer, r.PostForm.Get("grant_type"))
		tok, err := jwt.Parse(r.PostForm.Get("assertion"), func(tk *jwt.Token) (any, error) {
			assert.Equal(t, "k1", tk.Header["kid"])
			return &priv.PublicKey, nil
		}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience("http://"+r.Host+"/iam/token"))
		require.NoError(t, err, "assertion vérifiable avec la clé publique")
		claims := tok.Claims.(jwt.MapClaims)
		assert.Equal(t, "serviceAccount:sa-1", claims["iss"])
		assert.Equal(t, "serviceAccount:sa-1", claims["sub"])
		assert.NotEmpty(t, claims["jti"])
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "expires_in": 3600})
	}))
	defer sts.Close()

	path := filepath.Join(t.TempDir(), "key.json")
	raw, _ := json.Marshal(KeyFile{Type: keyFileType, ProjectID: "p-1", Principal: "serviceAccount:sa-1", KeyID: "k1",
		Algorithm: "ES256", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		TokenURI: sts.URL + "/iam/token"})
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	src, err := KeyFileSource(path)
	require.NoError(t, err)
	assert.Contains(t, src.Describe(), "serviceAccount:sa-1")
	assert.Equal(t, "p-1", KeyFileProject(path))
	for range 3 {
		tok, err := src.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "at-1", tok)
	}
	assert.Equal(t, int32(1), calls.Load(), "jeton en cache")
	src.Invalidate()
	_, err = src.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())

	require.NoError(t, os.WriteFile(path, []byte(`{"type":"autre"}`), 0o600))
	_, err = KeyFileSource(path)
	assert.Error(t, err)
}

// GitHub Actions : jeton OIDC demandé pour l'audience découverte auprès du
// STS, puis échangé (RFC 8693) pour le compte de service désigné.
func TestExchangeSource_GitHub(t *testing.T) {
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/iam/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer": "https://api.betorun.app/v1/iam", "token_endpoint": api.URL + "/v1/iam/token"})
		case "/oidc":
			assert.Equal(t, "Bearer gh-request", r.Header.Get("Authorization"))
			assert.Equal(t, "https://api.betorun.app/v1/iam", r.URL.Query().Get("audience"), "audience = émetteur du STS")
			_ = json.NewEncoder(w).Encode(map[string]string{"value": "gh-oidc"})
		case "/v1/iam/token":
			require.NoError(t, r.ParseForm())
			assert.Equal(t, grantTokenExchange, r.PostForm.Get("grant_type"))
			assert.Equal(t, "gh-oidc", r.PostForm.Get("subject_token"))
			assert.Equal(t, tokenTypeJWT, r.PostForm.Get("subject_token_type"))
			assert.Equal(t, "serviceAccount:sa-1", r.PostForm.Get("service_account"))
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-ci", "expires_in": 900})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	src := ExchangeSource(api.URL+"/v1", "", "serviceAccount:sa-1", "GitHub Actions",
		GitHubSubject(api.URL+"/oidc?x=1", "gh-request", nil))
	tok, err := src.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "at-ci", tok)
	assert.Equal(t, "serviceAccount:sa-1 (GitHub Actions)", src.Describe())
}

// Refus du STS : erreur OAuth lisible (code et description).
func TestExchangeSource_Refused(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/iam/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": "iss", "token_endpoint": "http://" + r.Host + "/iam/token"})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "no trust matches"})
	}))
	defer api.Close()
	t.Setenv("BETORUN_ID_TOKEN", "oidc")
	_, err := ExchangeSource(api.URL, "", "", "OIDC", EnvSubject("BETORUN_ID_TOKEN")).Token(context.Background())
	var stsErr *STSError
	require.ErrorAs(t, err, &stsErr)
	assert.Equal(t, "invalid_grant", stsErr.Code)
	assert.Contains(t, err.Error(), "no trust matches")
}

// Package social : connexion avec Google ou GitHub, sans intermédiaire
// (docs/architecture/auth.md §8). betorun parle directement au fournisseur
// (Authorization Code + PKCE, state) et n'en retient que l'identité.
package social

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v2"
	"github.com/golang-jwt/jwt/v5"
)

// Fournisseurs pris en charge.
const (
	Google = "google"
	GitHub = "github"
)

var (
	ErrUnknownProvider = errors.New("unknown sign-in provider")
	ErrProviderRefused = errors.New("the provider did not confirm your identity")
	ErrEmailUnverified = errors.New("your email address is not verified with this provider")
)

// Profile : identité attestée par le fournisseur.
type Profile struct {
	Provider      string
	Subject       string // identifiant stable chez le fournisseur
	Email         string
	EmailVerified bool
	Name          string
	AvatarURL     string
}

// Provider : un fournisseur d'identité.
type Provider interface {
	Name() string
	// AuthCodeURL : page du fournisseur où la personne se connecte.
	AuthCodeURL(state, challenge, nonce, redirectURI string) string
	// Exchange : code reçu contre l'identité vérifiée de la personne.
	Exchange(ctx context.Context, code, verifier, nonce, redirectURI string) (*Profile, error)
}

// Config : application OAuth enregistrée chez le fournisseur. Les adresses
// ont des valeurs par défaut (réglables pour les tests).
type Config struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	APIURL       string // GitHub : API ; Google : inutilisé
	JWKSURL      string // Google
	Issuer       string // Google
}

// NewPKCE : vérificateur (32 octets aléatoires) et empreinte S256.
func NewPKCE() (verifier, challenge string, err error) {
	verifier, err = randomString()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewState : valeur imprévisible (state, nonce).
func NewState() (string, error) { return randomString() }

var httpClient = &http.Client{Timeout: 15 * time.Second}

func exchangeCode(ctx context.Context, cfg Config, code, verifier, redirectURI string) (map[string]any, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out map[string]any
	if err := doJSON(req, &out); err != nil {
		return nil, err
	}
	if e, _ := out["error"].(string); e != "" {
		return nil, fmt.Errorf("%w: %s", ErrProviderRefused, e)
	}
	return out, nil
}

func doJSON(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("social: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: %s", ErrProviderRefused, resp.Status)
	}
	return json.Unmarshal(raw, out)
}

// ── Google (OpenID Connect) ──

type google struct {
	cfg  Config
	mu   sync.Mutex
	jwks *keyfunc.JWKS
}

func NewGoogle(cfg Config) Provider {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&cfg.AuthURL, "https://accounts.google.com/o/oauth2/v2/auth")
	def(&cfg.TokenURL, "https://oauth2.googleapis.com/token")
	def(&cfg.JWKSURL, "https://www.googleapis.com/oauth2/v3/certs")
	def(&cfg.Issuer, "https://accounts.google.com")
	return &google{cfg: cfg}
}

func (g *google) Name() string { return Google }

func (g *google) AuthCodeURL(state, challenge, nonce, redirectURI string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {g.cfg.ClientID}, "redirect_uri": {redirectURI},
		"scope": {"openid email profile"}, "state": {state}, "nonce": {nonce},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "prompt": {"select_account"}}
	return g.cfg.AuthURL + "?" + q.Encode()
}

func (g *google) keys() (*keyfunc.JWKS, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.jwks != nil {
		return g.jwks, nil
	}
	k, err := keyfunc.Get(g.cfg.JWKSURL, keyfunc.Options{RefreshInterval: time.Hour, RefreshUnknownKID: true,
		RefreshTimeout: 10 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("social: google keys: %w", err)
	}
	g.jwks = k
	return k, nil
}

func (g *google) Exchange(ctx context.Context, code, verifier, nonce, redirectURI string) (*Profile, error) {
	tok, err := exchangeCode(ctx, g.cfg, code, verifier, redirectURI)
	if err != nil {
		return nil, err
	}
	raw, _ := tok["id_token"].(string)
	if raw == "" {
		return nil, ErrProviderRefused
	}
	keys, err := g.keys()
	if err != nil {
		return nil, err
	}
	var c struct {
		jwt.RegisteredClaims
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		Nonce         string `json:"nonce"`
	}
	// Google émet « https://accounts.google.com » ou « accounts.google.com ».
	_, err = jwt.ParseWithClaims(raw, &c, keys.Keyfunc, jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithAudience(g.cfg.ClientID), jwt.WithExpirationRequired(), jwt.WithLeeway(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderRefused, err)
	}
	if iss := strings.TrimPrefix(c.Issuer, "https://"); iss != strings.TrimPrefix(g.cfg.Issuer, "https://") {
		return nil, ErrProviderRefused
	}
	if c.Nonce == "" || c.Nonce != nonce {
		return nil, ErrProviderRefused
	}
	return &Profile{Provider: Google, Subject: c.Subject, Email: strings.ToLower(c.Email), EmailVerified: c.EmailVerified,
		Name: c.Name, AvatarURL: c.Picture}, nil
}

// ── GitHub (OAuth 2.0 + API) ──

type github struct{ cfg Config }

func NewGitHub(cfg Config) Provider {
	if cfg.AuthURL == "" {
		cfg.AuthURL = "https://github.com/login/oauth/authorize"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://github.com/login/oauth/access_token"
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.github.com"
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	return &github{cfg: cfg}
}

func (h *github) Name() string { return GitHub }

func (h *github) AuthCodeURL(state, challenge, _ string, redirectURI string) string {
	q := url.Values{"client_id": {h.cfg.ClientID}, "redirect_uri": {redirectURI}, "scope": {"read:user user:email"},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "allow_signup": {"true"}}
	return h.cfg.AuthURL + "?" + q.Encode()
}

func (h *github) api(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.APIURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return doJSON(req, out)
}

func (h *github) Exchange(ctx context.Context, code, verifier, _ string, redirectURI string) (*Profile, error) {
	tok, err := exchangeCode(ctx, h.cfg, code, verifier, redirectURI)
	if err != nil {
		return nil, err
	}
	access, _ := tok["access_token"].(string)
	if access == "" {
		return nil, ErrProviderRefused
	}
	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := h.api(ctx, access, "/user", &user); err != nil {
		return nil, err
	}
	if user.ID == 0 {
		return nil, ErrProviderRefused
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := h.api(ctx, access, "/user/emails", &emails); err != nil {
		return nil, err
	}
	p := &Profile{Provider: GitHub, Subject: fmt.Sprint(user.ID), Name: firstNonEmpty(user.Name, user.Login), AvatarURL: user.AvatarURL}
	for _, e := range emails {
		if e.Primary {
			p.Email, p.EmailVerified = strings.ToLower(e.Email), e.Verified
		}
	}
	return p, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

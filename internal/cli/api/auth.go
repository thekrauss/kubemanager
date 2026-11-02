package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/thekrauss/kubemanager/internal/cli/config"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// Connexion de la CLI gérée par betorun (docs/architecture/auth.md §9) :
// navigateur (RFC 8252 + PKCE) ou code d'appareil (RFC 8628). Aucune page
// ni jeton d'un fournisseur tiers.

// DeviceCode : réponse de POST /identity/device/code (RFC 8628, type de l'API).
type DeviceCode = identitytypes.DeviceCodeResponse

// DefaultDeviceInterval : délai entre deux sondages si le serveur n'en donne
// pas (RFC 8628 §3.5 : 5 s).
var DefaultDeviceInterval = 5 * time.Second

var (
	ErrDeviceExpired = errors.New("device code expired")
	ErrDeviceDenied  = errors.New("device login denied")
)

func credentialsFrom(out *identitytypes.AuthResponse, now time.Time) *config.Credentials {
	return &config.Credentials{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken,
		ExpiresAt: now.Add(time.Duration(out.ExpiresIn) * time.Second)}
}

func (c *Client) StartDeviceLogin(ctx context.Context) (*DeviceCode, error) {
	var out DeviceCode
	return &out, c.DoPublic(ctx, http.MethodPost, pathIdentityDeviceCode, nil, &out)
}

// WaitDeviceLogin : sonde jusqu'à la validation dans la console (RFC 8628 §3.5).
func (c *Client) WaitDeviceLogin(ctx context.Context, dc *DeviceCode) (*config.Credentials, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = DefaultDeviceInterval
	}
	deadline := c.now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		var out identitytypes.AuthResponse
		err := c.DoPublic(ctx, http.MethodPost, pathIdentityDeviceToken, map[string]string{"device_code": dc.DeviceCode}, &out)
		var apiErr *Error
		switch {
		case err == nil:
			return credentialsFrom(&out, c.now()), nil
		case errors.As(err, &apiErr) && apiErr.Code == deviceErrAuthorizationPending:
		case errors.As(err, &apiErr) && apiErr.Code == deviceErrSlowDown:
			interval += deviceSlowDownStep
		case errors.As(err, &apiErr) && apiErr.Code == deviceErrExpiredToken:
			return nil, ErrDeviceExpired
		case errors.As(err, &apiErr) && apiErr.Code == deviceErrAccessDenied:
			return nil, ErrDeviceDenied
		default:
			return nil, err
		}
		if c.now().After(deadline) {
			return nil, ErrDeviceExpired
		}
	}
}

// UseCredentials : session à utiliser (juste après la connexion).
func (c *Client) UseCredentials(creds *config.Credentials) {
	c.mu.Lock()
	c.creds = creds
	c.mu.Unlock()
}

// Logout : révoque la session côté serveur.
func (c *Client) Logout(ctx context.Context, refreshToken string) error {
	return c.Do(ctx, http.MethodPost, pathIdentityLogout, identitytypes.LogoutRequest{RefreshToken: refreshToken}, nil)
}

// BrowserLoginURL : page de la console où autoriser la CLI. challenge et
// state sont générés par la CLI (le vérificateur PKCE ne quitte pas la machine).
func (c *Client) BrowserLoginURL(ctx context.Context, redirectURI, challenge, state string) (string, error) {
	q := url.Values{"redirect_uri": {redirectURI}, "code_challenge": {challenge}, "state": {state}}
	var out identitytypes.URLResponse
	err := c.DoPublic(ctx, http.MethodGet, pathIdentityCLIAuthorizeURL+"?"+q.Encode(), nil, &out)
	return out.URL, err
}

// BrowserLoginToken : échange du code reçu par le serveur local (preuve PKCE).
func (c *Client) BrowserLoginToken(ctx context.Context, code, verifier, redirectURI string) (*config.Credentials, error) {
	var out identitytypes.AuthResponse
	err := c.DoPublic(ctx, http.MethodPost, pathIdentityCLIToken,
		identitytypes.CLITokenRequest{Code: code, CodeVerifier: verifier, RedirectURI: redirectURI}, &out)
	if err != nil {
		return nil, err
	}
	return credentialsFrom(&out, c.now()), nil
}

// VerifySession : second facteur vérifié dans la session courante (step-up) ;
// le nouveau jeton d'accès remplace l'actuel.
func (c *Client) VerifySession(ctx context.Context, code string) error {
	var out identitytypes.AuthResponse
	if err := c.Do(ctx, http.MethodPost, pathIdentitySessionVerify, identitytypes.StepUpRequest{Code: code}, &out); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.creds == nil {
		return nil
	}
	c.creds = &config.Credentials{AccessToken: out.AccessToken, RefreshToken: c.creds.RefreshToken,
		ExpiresAt: c.now().Add(time.Duration(out.ExpiresIn) * time.Second)}
	return config.SaveCredentials(c.creds)
}

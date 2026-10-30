package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/thekrauss/kubemanager/internal/cli/config"
	"github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// DeviceCode : réponse de POST /identity/device/code (RFC 8628, type de l'API).
type DeviceCode = keycloak.DeviceAuthorization

// DefaultDeviceInterval : délai entre deux sondages si le serveur n'en donne
// pas (RFC 8628 §3.5 : 5 s).
var DefaultDeviceInterval = 5 * time.Second

var (
	ErrDeviceExpired  = errors.New("device code expired")
	ErrDeviceDenied   = errors.New("device login denied")
	ErrDeviceDisabled = errors.New("device login disabled")
)

func (c *Client) StartDeviceLogin(ctx context.Context) (*DeviceCode, error) {
	var out DeviceCode
	err := c.DoPublic(ctx, http.MethodPost, pathIdentityDeviceCode, nil, &out)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotImplemented {
		return nil, ErrDeviceDisabled
	}
	return &out, err
}

// WaitDeviceLogin : sonde jusqu'à la validation dans le navigateur (RFC 8628 §3.5).
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
		var out identitytypes.RefreshTokenResponse
		err := c.DoPublic(ctx, http.MethodPost, pathIdentityDeviceToken, map[string]string{"device_code": dc.DeviceCode}, &out)
		var apiErr *Error
		switch {
		case err == nil:
			return &config.Credentials{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken,
				ExpiresAt: c.now().Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
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

// BrowserLoginURL : URL Keycloak pour une connexion de la CLI redirigée vers
// son serveur local (RFC 8252) ; stepUp exige une double authentification
// récente (réponse à STEP_UP_REQUIRED).
func (c *Client) BrowserLoginURL(ctx context.Context, redirectURI string, stepUp bool) (string, error) {
	q := url.Values{"redirect_uri": {redirectURI}, "client": {oidcClientCLI}}
	if stepUp {
		q.Set("step_up", "true")
	}
	var out struct {
		URL string `json:"url"`
	}
	err := c.DoPublic(ctx, http.MethodGet, pathIdentityOIDCAuthorize+"?"+q.Encode(), nil, &out)
	return out.URL, err
}

// BrowserLoginCallback : échange du code reçu par le serveur local.
func (c *Client) BrowserLoginCallback(ctx context.Context, code, state, redirectURI string) (*config.Credentials, error) {
	q := url.Values{"code": {code}, "state": {state}, "redirect_uri": {redirectURI}}
	var out identitytypes.LoginResponse
	if err := c.DoPublic(ctx, http.MethodGet, pathIdentityOIDCCallback+"?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &config.Credentials{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken,
		ExpiresAt: c.now().Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
}

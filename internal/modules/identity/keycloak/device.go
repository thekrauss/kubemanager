package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Flux « device authorization » (RFC 8628) : la CLI affiche un code, la
// personne valide dans son navigateur (avec MFA), la CLI reçoit ses jetons.
// Le client Keycloak de la CLI est public, avec « OAuth 2.0 Device
// Authorization Grant » activé.

// DeviceAuthorization : réponse de l'endpoint d'autorisation device.
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceTokenError : réponse d'attente ou de refus (authorization_pending,
// slow_down, expired_token, access_denied), à relayer telle quelle.
type DeviceTokenError struct {
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

func (e *DeviceTokenError) Error() string { return "device flow: " + e.Code }

// StartDeviceAuthorization : PKCE (S256) en plus du flux device : le client
// de la CLI l'exige pour tous ses flux (il sert aussi au retour sur
// 127.0.0.1, RFC 8252).
func (c *Client) StartDeviceAuthorization(ctx context.Context, clientID, codeChallenge string) (*DeviceAuthorization, error) {
	form := url.Values{"client_id": {clientID}, "scope": {"openid profile email offline_access"}}
	if codeChallenge != "" {
		form.Set("code_challenge", codeChallenge)
		form.Set("code_challenge_method", "S256")
	}
	var out DeviceAuthorization
	status, body, err := c.postForm(ctx, "/protocol/openid-connect/auth/device", form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("keycloak device authorization failed with status %d", status)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PollDeviceToken : jetons une fois la connexion validée ; sinon *DeviceTokenError.
func (c *Client) PollDeviceToken(ctx context.Context, clientID, deviceCode, codeVerifier string) (*TokenPair, error) {
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id": {clientID}, "device_code": {strings.TrimSpace(deviceCode)}}
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}
	status, body, err := c.postForm(ctx, "/protocol/openid-connect/token", form)
	if err != nil {
		return nil, err
	}
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		var e DeviceTokenError
		if json.Unmarshal(body, &e) == nil && e.Code != "" {
			return nil, &e
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("keycloak device token failed with status %d", status)
	}
	var t userTokenResponse
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, err
	}
	return &TokenPair{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresIn: t.ExpiresIn}, nil
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/realms/%s%s", c.baseURL, url.PathEscape(c.realm), path), strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, err
}

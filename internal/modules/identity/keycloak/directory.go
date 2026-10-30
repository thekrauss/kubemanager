package keycloak

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Annuaire : appels serveur à serveur du backend (docs/architecture/auth.md).
// Rien de ce qui suit n'est jamais renvoyé tel quel à un client : ni jeton,
// ni message de Keycloak.

var (
	// ErrAccountDisabled : compte désactivé dans l'annuaire.
	ErrAccountDisabled = errors.New("account disabled")
	// ErrPasswordCheckUnavailable : client de vérification non configuré.
	ErrPasswordCheckUnavailable = errors.New("password verification is not configured")
	// ErrPasswordRejected : mot de passe refusé par la politique de l'annuaire.
	ErrPasswordRejected = errors.New("password rejected by policy")
)

// PasswordCheck : compte dont le mot de passe vient d'être vérifié.
type PasswordCheck struct {
	Subject       string
	Email         string
	Name          string
	EmailVerified bool
}

// VerifyPassword : vérifie le couple email / mot de passe (grant password du
// client confidentiel réservé au backend ; les jetons obtenus sont jetés).
// Échec : ErrInvalidCredentials, ErrAccountDisabled ou une erreur technique.
func (c *Client) VerifyPassword(ctx context.Context, email, password string) (*PasswordCheck, error) {
	if c.pwdClientID == "" || c.pwdSecret == "" {
		return nil, ErrPasswordCheckUnavailable
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", c.pwdClientID)
	form.Set("client_secret", c.pwdSecret)
	form.Set("username", email)
	form.Set("password", password)
	form.Set("scope", "openid email profile")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.baseURL, url.PathEscape(c.realm)),
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(raw, &e)
		switch {
		case strings.Contains(strings.ToLower(e.Description), "disabled"):
			return nil, ErrAccountDisabled
		case e.Error == "invalid_grant" || resp.StatusCode == http.StatusUnauthorized:
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("password verification failed with status %d", resp.StatusCode)
	}
	var tok userTokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, err
	}
	claims, err := unverifiedClaims(tok.AccessToken)
	if err != nil {
		return nil, err
	}
	return &PasswordCheck{Subject: claims.Sub, Email: firstNonEmpty(claims.Email, email), Name: claims.Name,
		EmailVerified: claims.EmailVerified}, nil
}

// unverifiedClaims : contenu d'un jeton reçu directement de l'annuaire, en
// réponse à notre propre requête TLS (sa signature n'apporte rien de plus).
func unverifiedClaims(token string) (*struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	EmailVerified bool   `json:"email_verified"`
}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("keycloak: malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("keycloak: token payload: %w", err)
	}
	var c struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		Name          string `json:"name"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, err
	}
	if c.Sub == "" {
		return nil, errors.New("keycloak: token without subject")
	}
	return &c, nil
}

// MarkEmailVerified : adresse confirmée par un lien envoyé par betorun.
func (c *Client) MarkEmailVerified(ctx context.Context, subject string) error {
	return c.updateUser(ctx, subject, map[string]any{"emailVerified": true})
}

// SetPassword : nouveau mot de passe ; ErrPasswordRejected si la politique
// de l'annuaire le refuse (longueur, complexité, historique).
func (c *Client) SetPassword(ctx context.Context, subject, password string) error {
	token, err := c.adminAccessToken(ctx)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(credentialRepresentation{Type: "password", Value: password, Temporary: false})
	status, body, err := c.adminDo(ctx, token, http.MethodPut,
		fmt.Sprintf("/admin/realms/%s/users/%s/reset-password", url.PathEscape(c.realm), url.PathEscape(subject)), payload)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusNoContent || status == http.StatusOK:
		return nil
	case status == http.StatusBadRequest:
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return fmt.Errorf("%w: %s", ErrPasswordRejected, e.Error)
	case status == http.StatusNotFound:
		return ErrUserNotFound
	}
	return fmt.Errorf("set password failed with status %d", status)
}

func (c *Client) updateUser(ctx context.Context, subject string, fields map[string]any) error {
	token, err := c.adminAccessToken(ctx)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(fields)
	status, _, err := c.adminDo(ctx, token, http.MethodPut,
		fmt.Sprintf("/admin/realms/%s/users/%s", url.PathEscape(c.realm), url.PathEscape(subject)), payload)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusNotFound:
		return ErrUserNotFound
	}
	return fmt.Errorf("update user failed with status %d", status)
}

func (c *Client) adminDo(ctx context.Context, token, method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

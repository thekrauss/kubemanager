// Package api : client HTTP de l'API betorun.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thekrauss/kubemanager/internal/cli/config"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// Error : réponse d'erreur de l'API (message déjà traduit par l'API).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	switch {
	case e.Code == "STEP_UP_REQUIRED":
		return i18n.T("err.step_up")
	case e.Status == http.StatusForbidden:
		return i18n.T("err.forbidden", e.Message)
	}
	return i18n.T("err.api", e.Status, e.Message)
}

var (
	ErrNotLoggedIn    = errors.New("not logged in")
	ErrSessionExpired = errors.New("session expired")
)

// Client : appels authentifiés, renouvellement automatique de la session.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// StaticToken : jeton fourni tel quel (BETORUN_TOKEN), sans session.
	StaticToken string
	now         func() time.Time

	mu    sync.Mutex
	creds *config.Credentials
}

func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 5 * time.Minute}, now: time.Now}
}

// token : jeton d'accès valide, renouvelé 30 s avant son expiration.
func (c *Client) token(ctx context.Context) (string, error) {
	if c.StaticToken != "" {
		return c.StaticToken, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.creds == nil {
		creds, err := config.LoadCredentials()
		if err != nil {
			return "", err
		}
		if creds == nil {
			return "", ErrNotLoggedIn
		}
		c.creds = creds
	}
	if c.now().Add(30 * time.Second).Before(c.creds.ExpiresAt) {
		return c.creds.AccessToken, nil
	}
	var out identitytypes.RefreshTokenResponse
	err := c.do(ctx, http.MethodPost, "/identity/refresh", "", identitytypes.RefreshTokenRequest{RefreshToken: c.creds.RefreshToken}, &out)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status < 500 {
		return "", ErrSessionExpired
	}
	if err != nil {
		return "", err
	}
	c.creds = &config.Credentials{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken,
		ExpiresAt: c.now().Add(time.Duration(out.ExpiresIn) * time.Second)}
	if err := config.SaveCredentials(c.creds); err != nil {
		return "", err
	}
	return c.creds.AccessToken, nil
}

// Do : appel authentifié ; body JSON (ou nil), réponse décodée dans out (ou nil).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path, tok, body, out)
}

// DoPublic : appel sans authentification (connexion).
func (c *Client) DoPublic(ctx context.Context, method, path string, body, out any) error {
	return c.do(ctx, method, path, "", body, out)
}

// Upload : envoi multipart (champ field) authentifié.
func (c *Client) Upload(ctx context.Context, path, field, filename string, data io.Reader, out any) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	mw := newMultipart(pw)
	go func() {
		pw.CloseWithError(mw.writeFile(field, filename, data))
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.contentType())
	return c.send(req, tok, out)
}

func (c *Client) do(ctx context.Context, method, path, tok string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, tok, out)
}

func (c *Client) send(req *http.Request, tok string, out any) error {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", i18n.Lang())
	req.Header.Set("User-Agent", "betorun-cli")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s", i18n.T("err.network", c.BaseURL, err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return decodeError(resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func decodeError(status int, raw []byte) error {
	var body struct {
		Code             string `json:"code"`
		Message          string `json:"message"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &body)
	msg := body.Message
	if msg == "" {
		msg = body.ErrorDescription
	}
	if msg == "" {
		msg = body.Error
	}
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	code := body.Code
	if code == "" {
		code = body.Error // erreurs OAuth : authorization_pending, slow_down…
	}
	return &Error{Status: status, Code: code, Message: msg}
}

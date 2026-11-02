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
	"github.com/thekrauss/kubemanager/internal/core/httpheader"
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
	case e.Code == errCodeStepUpRequired:
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
	// Source : identifiants machine (jeton fixe, fichier de clé, CI, pod) ;
	// nil : la session de « betorun login ».
	Source TokenSource
	// OnStepUp : appelé quand l'API exige une double authentification
	// récente (STEP_UP_REQUIRED) ; s'il réussit, la requête est rejouée une
	// fois. nil : l'erreur est renvoyée (terminal non interactif, CI).
	OnStepUp func(ctx context.Context) error
	now      func() time.Time

	mu    sync.Mutex
	creds *config.Credentials
}

func New(baseURL string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Vaut aussi pour les envois, sans délai global : serveur muet une fois le
	// corps reçu.
	transport.ResponseHeaderTimeout = httpTimeout
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: httpTimeout, Transport: transport}, now: time.Now}
}

// uploadClient : même transport, sans délai global. Sa durée dépend du débit de
// l'utilisateur (100 Mo à 1 Mbit/s : 13 min) ; il s'interrompt par le contexte.
func (c *Client) uploadClient() *http.Client {
	upload := *c.HTTP
	upload.Timeout = 0
	return &upload
}

// token : jeton d'accès valide, renouvelé 30 s avant son expiration.
func (c *Client) token(ctx context.Context) (string, error) {
	if c.Source != nil {
		return c.Source.Token(ctx)
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
	if c.now().Add(sessionRefreshBefore).Before(c.creds.ExpiresAt) {
		return c.creds.AccessToken, nil
	}
	var out identitytypes.RefreshTokenResponse
	err := c.do(ctx, http.MethodPost, pathIdentityRefresh, "", identitytypes.RefreshTokenRequest{RefreshToken: c.creds.RefreshToken}, &out)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status < 500 {
		return "", ErrSessionExpired
	}
	if err != nil {
		return "", err
	}
	refresh := out.RefreshToken
	if refresh == "" {
		// Jeton tout juste remplacé par une requête simultanée : l'actuel reste valable.
		refresh = c.creds.RefreshToken
	}
	c.creds = &config.Credentials{AccessToken: out.AccessToken, RefreshToken: refresh,
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
	err = c.do(ctx, method, path, tok, body, out)
	var apiErr *Error
	if c.Source != nil && errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
		// Jeton machine refusé (clé tournée, relation de confiance modifiée) :
		// un nouveau jeton, une seule fois.
		c.Source.Invalidate()
		if tok, err = c.token(ctx); err != nil {
			return err
		}
		err = c.do(ctx, method, path, tok, body, out)
	}
	if c.OnStepUp == nil || !errors.As(err, &apiErr) || apiErr.Code != errCodeStepUpRequired {
		return err
	}
	// Confirmation d'identité, puis la même requête avec la nouvelle session.
	if serr := c.OnStepUp(ctx); serr != nil {
		return serr
	}
	if tok, err = c.token(ctx); err != nil {
		return err
	}
	return c.do(ctx, method, path, tok, body, out)
}

// AccessToken : jeton d'accès courant (scripts : curl, outils tiers).
func (c *Client) AccessToken(ctx context.Context) (string, error) { return c.token(ctx) }

// DoPublic : appel sans authentification (connexion).
func (c *Client) DoPublic(ctx context.Context, method, path string, body, out any) error {
	return c.do(ctx, method, path, "", body, out)
}

// Upload : envoi multipart (champ field) authentifié.
func (c *Client) Upload(ctx context.Context, path, field, filename string, data io.Reader, out any) error {
	return c.UploadWithFields(ctx, path, nil, field, filename, data, out)
}

// UploadWithFields : envoi multipart avec des champs avant le fichier.
func (c *Client) UploadWithFields(ctx context.Context, path string, fields map[string]string, field, filename string, data io.Reader, out any) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	mw := newMultipart(pw)
	go func() {
		for k, v := range fields {
			if err := mw.writeField(k, v); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.CloseWithError(mw.writeFile(field, filename, data))
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, pr)
	if err != nil {
		return err
	}
	req.Header.Set(httpheader.ContentType, mw.contentType())
	return c.sendWith(c.uploadClient(), req, tok, out)
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
		req.Header.Set(httpheader.ContentType, httpheader.MIMEJSON)
	}
	return c.send(req, tok, out)
}

func (c *Client) send(req *http.Request, tok string, out any) error {
	return c.sendWith(c.HTTP, req, tok, out)
}

func (c *Client) sendWith(client *http.Client, req *http.Request, tok string, out any) error {
	req.Header.Set(httpheader.Accept, httpheader.MIMEJSON)
	req.Header.Set(httpheader.AcceptLanguage, i18n.Lang())
	req.Header.Set(httpheader.UserAgent, userAgent)
	if tok != "" {
		req.Header.Set(httpheader.Authorization, bearerPrefix+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s", i18n.T("err.network", c.BaseURL, err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
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

// Stream : GET authentifié dont le corps est copié vers w au fil de l'eau,
// sans délai maximal (un suivi de logs peut durer ; Ctrl-C l'interrompt).
func (c *Client) Stream(ctx context.Context, path string, w io.Writer) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set(httpheader.AcceptLanguage, i18n.Lang())
	req.Header.Set(httpheader.UserAgent, userAgent)
	req.Header.Set(httpheader.Authorization, bearerPrefix+tok)
	resp, err := (&http.Client{Transport: c.HTTP.Transport}).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("%s", i18n.T("err.network", c.BaseURL, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxStreamErrorBytes))
		return decodeError(resp.StatusCode, raw)
	}
	_, err = io.Copy(w, resp.Body)
	if ctx.Err() != nil {
		return nil // interrompu par l'utilisateur
	}
	return err
}

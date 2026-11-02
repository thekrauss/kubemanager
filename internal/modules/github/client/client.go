// Package client : API de GitHub vue par l'application GitHub de betorun.
// Aucune valeur secrète (clé, jeton, code) n'apparaît dans une erreur : seuls
// le chemin appelé et le statut HTTP y figurent.
package client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Erreurs renvoyées par le client.
var (
	// ErrNotAccessible : GitHub refuse ou ne connaît pas (401/403/404/422).
	ErrNotAccessible = errors.New("github: not accessible")
	// ErrUnavailable : réseau, 5xx, limite de débit.
	ErrUnavailable = errors.New("github: unavailable")
	// ErrBadCode : code OAuth refusé.
	ErrBadCode = errors.New("github: authorization code refused")
)

const (
	apiVersion      = "2022-11-28"
	userAgent       = "betorun"
	httpTimeout     = 15 * time.Second
	maxResponse     = 8 << 20
	jwtBackdate     = 60 * time.Second // horloges décalées (recommandation GitHub)
	jwtLifetime     = 9 * time.Minute  // GitHub refuse au-delà de 10 minutes
	listPageSize    = 100
	maxUserInstPage = 10
)

var fullNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// Config : application GitHub enregistrée.
type Config struct {
	AppID, Slug, ClientID, ClientSecret string
	PrivateKeyPEM                       []byte
	APIURL, WebURL                      string
}

// Client : appels à GitHub.
type Client struct {
	cfg  Config
	key  *rsa.PrivateKey
	http *http.Client
	now  func() time.Time
}

// LoadKey : clé privée de l'application (fichier PEM, hors du code).
func LoadKey(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// New : configuration vérifiée (clé RSA lisible).
func New(cfg Config) (*Client, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, errors.New("github app: invalid RSA private key")
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	cfg.WebURL = strings.TrimRight(cfg.WebURL, "/")
	return &Client{cfg: cfg, key: key, http: &http.Client{Timeout: httpTimeout}, now: time.Now}, nil
}

// appJWT : identité de l'application (RS256). Ne donne accès qu'à l'API /app.
func (c *Client) appJWT() (string, error) {
	now := c.now()
	claims := jwt.RegisteredClaims{
		Issuer:    c.cfg.AppID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-jwtBackdate)),
		ExpiresAt: jwt.NewNumericDate(now.Add(jwtLifetime)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(c.key)
}

// InstallURL : page de GitHub où installer l'application.
func (c *Client) InstallURL(state string) string {
	return c.cfg.WebURL + "/apps/" + url.PathEscape(c.cfg.Slug) + "/installations/new?state=" + url.QueryEscape(state)
}

// CloneURL : adresse de clonage, sans identifiants, à partir du web_url
// configuré et du nom renvoyé par l'API (donc jamais d'une saisie).
func (c *Client) CloneURL(fullName string) string {
	return c.cfg.WebURL + "/" + fullName + ".git"
}

// HTMLURL : page du dépôt.
func (c *Client) HTMLURL(fullName string) string { return c.cfg.WebURL + "/" + fullName }

// Slug : nom public de l'application.
func (c *Client) Slug() string { return c.cfg.Slug }

func (c *Client) do(ctx context.Context, method, target, bearer string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: connection failed", ErrUnavailable, method, req.URL.Path)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("%w: %s %s: read failed", ErrUnavailable, method, req.URL.Path)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		return fmt.Errorf("%w: %s %s: %d", ErrUnavailable, method, req.URL.Path, resp.StatusCode)
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return fmt.Errorf("%w: %s %s: rate limited", ErrUnavailable, method, req.URL.Path)
	case resp.StatusCode >= 400:
		return fmt.Errorf("%w: %s %s: %d", ErrNotAccessible, method, req.URL.Path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %s %s: invalid response", ErrUnavailable, method, req.URL.Path)
	}
	return nil
}

// TokenRequest : portée d'un jeton d'installation. Vide : tous les dépôts de
// l'installation, avec toutes les permissions accordées à l'application.
type TokenRequest struct {
	RepositoryIDs []int64           `json:"repository_ids,omitempty"`
	Permissions   map[string]string `json:"permissions,omitempty"`
}

// CloneScope : lecture du code d'un seul dépôt. Le code du client peut lire ce
// jeton pendant son build (voir builds/activities.prepareGitHubSource).
func CloneScope(repoID int64) TokenRequest {
	return TokenRequest{RepositoryIDs: []int64{repoID}, Permissions: map[string]string{"contents": "read"}}
}

// ListScope : noms des dépôts, sans leur contenu.
func ListScope() TokenRequest {
	return TokenRequest{Permissions: map[string]string{"metadata": "read"}}
}

// InstallationToken : jeton d'installation à la demande, jamais conservé.
func (c *Client) InstallationToken(ctx context.Context, installationID int64, scope TokenRequest) (string, error) {
	appToken, err := c.appJWT()
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	target := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.cfg.APIURL, installationID)
	if err := c.do(ctx, http.MethodPost, target, appToken, scope, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("%w: empty token", ErrUnavailable)
	}
	return out.Token, nil
}

// ExchangeCode : code reçu au retour de GitHub contre un jeton utilisateur de
// l'application. Ce jeton ne sert qu'à la preuve d'accès : à ne pas conserver.
func (c *Client) ExchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{"client_id": {c.cfg.ClientID}, "client_secret": {c.cfg.ClientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.WebURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: code exchange: connection failed", ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("%w: code exchange: %d", ErrUnavailable, resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&out); err != nil || out.Error != "" || out.AccessToken == "" {
		return "", ErrBadCode
	}
	return out.AccessToken, nil
}

// Account : compte qui a installé l'application.
type Account struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// Installation : installation de l'application.
type Installation struct {
	ID      int64   `json:"id"`
	Account Account `json:"account"`
}

// UserInstallation : l'installation demandée, si cette personne y a accès
// (preuve : son propre jeton, GET /user/installations). nil sinon.
func (c *Client) UserInstallation(ctx context.Context, userToken string, installationID int64) (*Installation, error) {
	for page := 1; page <= maxUserInstPage; page++ {
		var out struct {
			Installations []Installation `json:"installations"`
		}
		target := fmt.Sprintf("%s/user/installations?per_page=%d&page=%d", c.cfg.APIURL, listPageSize, page)
		if err := c.do(ctx, http.MethodGet, target, userToken, nil, &out); err != nil {
			return nil, err
		}
		for i := range out.Installations {
			if out.Installations[i].ID == installationID {
				return &out.Installations[i], nil
			}
		}
		if len(out.Installations) < listPageSize {
			break
		}
	}
	return nil, nil
}

// Repository : dépôt vu par une installation.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	PushedAt      string `json:"pushed_at,omitempty"`
}

// Repositories : dépôts accessibles à une installation, une page à la fois.
func (c *Client) Repositories(ctx context.Context, token string, page, perPage int) ([]Repository, int, error) {
	var out struct {
		TotalCount   int          `json:"total_count"`
		Repositories []Repository `json:"repositories"`
	}
	target := fmt.Sprintf("%s/installation/repositories?per_page=%d&page=%d", c.cfg.APIURL, perPage, page)
	if err := c.do(ctx, http.MethodGet, target, token, nil, &out); err != nil {
		return nil, 0, err
	}
	return out.Repositories, out.TotalCount, nil
}

// RepositoryByID : dépôt désigné par son identifiant, avec son nom actuel.
func (c *Client) RepositoryByID(ctx context.Context, token string, id int64) (*Repository, error) {
	var out Repository
	if err := c.do(ctx, http.MethodGet, c.cfg.APIURL+"/repositories/"+strconv.FormatInt(id, 10), token, nil, &out); err != nil {
		return nil, err
	}
	if !fullNameRe.MatchString(out.FullName) {
		return nil, fmt.Errorf("%w: unexpected repository name", ErrUnavailable)
	}
	return &out, nil
}

// Branch : branche et commit le plus récent.
type Branch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// Branches : branches d'un dépôt, une page à la fois.
func (c *Client) Branches(ctx context.Context, token, fullName string, page, perPage int) ([]Branch, error) {
	if !fullNameRe.MatchString(fullName) {
		return nil, ErrNotAccessible
	}
	var out []Branch
	target := fmt.Sprintf("%s/repos/%s/branches?per_page=%d&page=%d", c.cfg.APIURL, fullName, perPage, page)
	if err := c.do(ctx, http.MethodGet, target, token, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// BranchHead : une branche (ErrNotAccessible si elle n'existe pas).
func (c *Client) BranchHead(ctx context.Context, token, fullName, branch string) (*Branch, error) {
	if !fullNameRe.MatchString(fullName) {
		return nil, ErrNotAccessible
	}
	var out Branch
	target := c.cfg.APIURL + "/repos/" + fullName + "/branches/" + url.PathEscape(branch)
	if err := c.do(ctx, http.MethodGet, target, token, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SignatureValid : X-GitHub-Signature-256 (« sha256=<hex> ») sur le corps brut,
// comparée en temps constant.
func SignatureValid(secret string, body []byte, header string) bool {
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok || secret == "" {
		return false
	}
	want := hmac.New(sha256.New, []byte(secret))
	want.Write(body)
	return hmac.Equal([]byte(got), []byte(hex.EncodeToString(want.Sum(nil))))
}

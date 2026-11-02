package api

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/thekrauss/kubemanager/internal/cli/config"
)

// Identifiants machine : jetons courts obtenus auprès du STS de betorun,
// sans secret longue durée quand c'est possible. Une source se branche sur
// le client (Client.Source) ; en ajouter une ne touche pas au reste.

const (
	grantJWTBearer      = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	grantTokenExchange  = "urn:ietf:params:oauth:grant-type:token-exchange"
	tokenTypeJWT        = "urn:ietf:params:oauth:token-type:jwt"
	keyFileType         = "betorun_service_account_key"
	assertionTTL        = 5 * time.Minute
	tokenRefreshBefore  = 2 * time.Minute
	stsDiscoveryPath    = "/iam/.well-known/openid-configuration"
	githubTokenAudience = "audience"
)

// TokenSource : origine des jetons d'accès d'un client.
type TokenSource interface {
	// Token : jeton valide (en cache ou nouvellement obtenu).
	Token(ctx context.Context) (string, error)
	// Invalidate : jeton refusé par l'API (clé tournée…) : le suivant sera redemandé.
	Invalidate()
	// Describe : origine lisible (whoami), sans secret.
	Describe() string
}

// ── Jeton fixe (BETORUN_TOKEN) ────────────────────────────────────────────

type staticSource string

func (s staticSource) Token(context.Context) (string, error) { return string(s), nil }
func (staticSource) Invalidate()                             {}
func (staticSource) Describe() string                        { return config.EnvToken }

// StaticSource : jeton fourni tel quel.
func StaticSource(token string) TokenSource { return staticSource(token) }

// ── Cache commun aux sources STS ──────────────────────────────────────────

type stsCache struct {
	http  *http.Client
	now   func() time.Time
	mu    sync.Mutex
	token string
	exp   time.Time
}

func newSTSCache() stsCache {
	return stsCache{http: &http.Client{Timeout: 30 * time.Second}, now: time.Now}
}

func (c *stsCache) get(ctx context.Context, fetch func(ctx context.Context) (url.Values, string, error)) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Add(tokenRefreshBefore).Before(c.exp) {
		return c.token, nil
	}
	form, tokenURI, err := fetch(ctx)
	if err != nil {
		return "", err
	}
	tok, ttl, err := postSTS(ctx, c.http, tokenURI, form)
	if err != nil {
		return "", err
	}
	c.token, c.exp = tok, c.now().Add(ttl)
	return c.token, nil
}

func (c *stsCache) Invalidate() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// STSError : refus du STS (format OAuth).
type STSError struct {
	Status      int
	Code        string
	Description string
}

func (e *STSError) Error() string {
	return fmt.Sprintf("sts: %s: %s (HTTP %d)", e.Code, e.Description, e.Status)
}

func postSTS(ctx context.Context, hc *http.Client, tokenURI string, form url.Values) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("sts: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int    `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, fmt.Errorf("sts: HTTP %d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return "", 0, &STSError{Status: resp.StatusCode, Code: body.Error, Description: body.ErrorDescription}
	}
	return body.AccessToken, time.Duration(body.ExpiresIn) * time.Second, nil
}

// ── Fichier de clé de compte de service (RFC 7523) ─────────────────────────

// KeyFile : fichier remis une seule fois à la création d'une clé.
type KeyFile struct {
	Type             string `json:"type"`
	ProjectID        string `json:"project_id,omitempty"`
	ClusterID        string `json:"cluster_id,omitempty"`
	ServiceAccountID string `json:"service_account_id"`
	Principal        string `json:"principal"`
	KeyID            string `json:"key_id"`
	Algorithm        string `json:"algorithm"`
	PrivateKey       string `json:"private_key"`
	TokenURI         string `json:"token_uri"`
}

type keyFileSource struct {
	stsCache
	path   string
	file   KeyFile
	method jwt.SigningMethod
	key    crypto.Signer
}

// KeyFileSource : jetons d'une heure obtenus en signant une assertion avec
// la clé privée du fichier ; la clé ne quitte jamais la machine.
func KeyFileSource(path string) (TokenSource, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("creds.key_file"), err)
	}
	var f KeyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s %s: %w", i18n.T("creds.key_file"), path, err)
	}
	if f.Type != keyFileType || f.KeyID == "" || f.Principal == "" || f.TokenURI == "" {
		return nil, fmt.Errorf("key file %s: not a betorun service account key", path)
	}
	block, _ := pem.Decode([]byte(f.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("key file %s: private_key is not PEM", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", i18n.T("creds.key_file"), path, err)
	}
	s := &keyFileSource{stsCache: newSTSCache(), path: path, file: f}
	switch k := parsed.(type) {
	case *ecdsa.PrivateKey:
		s.method, s.key = jwt.SigningMethodES256, k
	case *rsa.PrivateKey:
		s.method, s.key = jwt.SigningMethodRS256, k
	default:
		return nil, fmt.Errorf("key file %s: unsupported key type", path)
	}
	return s, nil
}

func (s *keyFileSource) Describe() string {
	return fmt.Sprintf("%s (%s)", s.file.Principal, s.path)
}

func (s *keyFileSource) Token(ctx context.Context) (string, error) {
	return s.get(ctx, func(context.Context) (url.Values, string, error) {
		now := s.now()
		jti := make([]byte, 16)
		if _, err := rand.Read(jti); err != nil {
			return nil, "", err
		}
		t := jwt.NewWithClaims(s.method, jwt.MapClaims{
			"iss": s.file.Principal, "sub": s.file.Principal, "aud": s.file.TokenURI,
			"iat": now.Unix(), "exp": now.Add(assertionTTL).Unix(), "jti": hex.EncodeToString(jti),
		})
		t.Header["kid"] = s.file.KeyID
		assertion, err := t.SignedString(s.key)
		if err != nil {
			return nil, "", err
		}
		return url.Values{"grant_type": {grantJWTBearer}, "assertion": {assertion}}, s.file.TokenURI, nil
	})
}

// KeyFileProject : projet d'un fichier de clé (projet par défaut de la CLI) ;
// vide si illisible ou compte de cluster.
func KeyFileProject(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var f KeyFile
	_ = json.Unmarshal(raw, &f)
	return f.ProjectID
}

// ── Échange d'un jeton externe (RFC 8693) ──────────────────────────────────

// SubjectToken : jeton OIDC à échanger (CI, pod), pour l'audience donnée.
type SubjectToken func(ctx context.Context, audience string) (string, error)

type exchangeSource struct {
	stsCache
	apiURL         string
	tokenURI       string // vide : découvert auprès de l'API
	serviceAccount string // optionnel si un seul compte fait confiance au jeton
	origin         string
	subject        SubjectToken
}

// ExchangeSource : jetons courts obtenus contre un jeton OIDC externe, selon
// les relations de confiance du compte de service (aucun secret stocké).
// L'émetteur et l'endpoint du STS sont découverts auprès de l'API, sauf si
// tokenURI est fourni.
func ExchangeSource(apiURL, tokenURI, serviceAccount, origin string, subject SubjectToken) TokenSource {
	return &exchangeSource{stsCache: newSTSCache(), apiURL: strings.TrimRight(apiURL, "/"),
		tokenURI: tokenURI, serviceAccount: serviceAccount, origin: origin, subject: subject}
}

func (s *exchangeSource) Describe() string {
	if s.serviceAccount != "" {
		return s.serviceAccount + " (" + s.origin + ")"
	}
	return s.origin
}

func (s *exchangeSource) Token(ctx context.Context) (string, error) {
	return s.get(ctx, func(ctx context.Context) (url.Values, string, error) {
		issuer, tokenURI, err := s.discover(ctx)
		if err != nil {
			return nil, "", err
		}
		subject, err := s.subject(ctx, issuer)
		if err != nil {
			return nil, "", err
		}
		form := url.Values{"grant_type": {grantTokenExchange}, "subject_token": {subject},
			"subject_token_type": {tokenTypeJWT}}
		if s.serviceAccount != "" {
			form.Set("service_account", s.serviceAccount)
		}
		return form, tokenURI, nil
	})
}

// discover : émetteur (audience attendue des jetons externes) et endpoint du STS.
func (s *exchangeSource) discover(ctx context.Context) (issuer, tokenURI string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL+stsDiscoveryPath, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("sts discovery: %w", err)
	}
	defer resp.Body.Close()
	var meta struct {
		Issuer        string `json:"issuer"`
		TokenEndpoint string `json:"token_endpoint"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("sts discovery: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil || meta.Issuer == "" {
		return "", "", errors.New("sts discovery: invalid metadata")
	}
	if s.tokenURI != "" {
		return meta.Issuer, s.tokenURI, nil
	}
	return meta.Issuer, meta.TokenEndpoint, nil
}

// FileSubject : jeton lu dans un fichier à chaque échange (jeton Kubernetes
// projeté, renouvelé par le kubelet).
func FileSubject(path string) SubjectToken {
	return func(context.Context, string) (string, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
}

// EnvSubject : jeton fourni par la CI dans une variable (GitLab id_tokens).
func EnvSubject(name string) SubjectToken {
	return func(context.Context, string) (string, error) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("%s is empty", name)
	}
}

// GitHubSubject : jeton OIDC de GitHub Actions, demandé pour l'audience du
// STS (le job doit avoir « permissions: id-token: write »).
func GitHubSubject(requestURL, requestToken string, hc *http.Client) SubjectToken {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return func(ctx context.Context, audience string) (string, error) {
		u, err := url.Parse(requestURL)
		if err != nil {
			return "", fmt.Errorf("github oidc: %w", err)
		}
		q := u.Query()
		q.Set(githubTokenAudience, audience)
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+requestToken)
		resp, err := hc.Do(req)
		if err != nil {
			return "", fmt.Errorf("github oidc: %w", err)
		}
		defer resp.Body.Close()
		var body struct {
			Value string `json:"value"`
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("github oidc: HTTP %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Value == "" {
			return "", errors.New("github oidc: empty token")
		}
		return body.Value, nil
	}
}

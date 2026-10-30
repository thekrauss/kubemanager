package sts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MicahParks/keyfunc/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/iam"
)

// Échange d'un jeton OIDC externe (GitHub Actions, GitLab CI…) contre un
// jeton betorun (RFC 8693, grant token-exchange), selon les relations de
// confiance des comptes de service (iam.FederationTrust).
//
// Garde-fous :
//   - seuls les émetteurs d'une relation de confiance existante sont
//     contactés (pas de requête vers un émetteur choisi par l'attaquant) ;
//   - découverte et JWKS en https, jamais vers une adresse privée,
//     loopback ou link-local (SSRF) ;
//   - aud du jeton externe = émetteur du STS (le jeton a été demandé pour
//     betorun, pas rejoué depuis un autre service) ;
//   - jti à usage unique quand l'émetteur en fournit un ;
//   - jeton émis : 15 min, lié à la relation (supprimée → refusé sous 30 s).

const (
	GrantTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	TokenTypeJWT       = "urn:ietf:params:oauth:token-type:jwt"
	TokenTypeIDToken   = "urn:ietf:params:oauth:token-type:id_token"
	TokenTypeAccess    = "urn:ietf:params:oauth:token-type:access_token"

	// MethodFederation : jeton obtenu par fédération d'identité.
	MethodFederation = "federation"
	// FederatedTokenTTL : durée des jetons fédérés (iam.md §7.2).
	FederatedTokenTTL = 15 * time.Minute
	// trustRefPrefix : référence d'une relation dans le claim btr_key.
	trustRefPrefix = "trust:"
	// maxExternalTokenAge : un jeton externe plus vieux est refusé.
	maxExternalTokenAge = time.Hour
)

// ExchangeRequest : paramètres RFC 8693.
type ExchangeRequest struct {
	SubjectToken     string
	SubjectTokenType string
	// ServiceAccount : "serviceAccount:<uuid>", requis seulement si plusieurs
	// comptes font confiance à ce jeton.
	ServiceAccount string
}

// Issuers : clés publiques des émetteurs fédérés, mises en cache.
type Issuers struct {
	Client *http.Client

	mu   sync.Mutex
	jwks map[string]*keyfunc.JWKS
}

// NewIssuers : client http qui refuse les adresses internes (SSRF).
func NewIssuers() *Issuers {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: refusePrivate}
	return &Issuers{
		Client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
			DialContext: dialer.DialContext, TLSHandshakeTimeout: 5 * time.Second, MaxIdleConns: 20,
		}},
		jwks: map[string]*keyfunc.JWKS{},
	}
}

var errPrivateAddress = errors.New("federation: refusing to contact a private address")

func refusePrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return errPrivateAddress
	}
	return nil
}

// keyfunc : clés de l'émetteur (découverte OIDC puis JWKS, rafraîchies).
func (i *Issuers) keyfunc(ctx context.Context, issuer string) (jwt.Keyfunc, error) {
	i.mu.Lock()
	k, ok := i.jwks[issuer]
	i.mu.Unlock()
	if ok {
		return k.Keyfunc, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	resp, err := i.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("federation: discovery: %w", err)
	}
	defer resp.Body.Close()
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(&doc) != nil {
		return nil, fmt.Errorf("federation: discovery failed for %s (status %d)", issuer, resp.StatusCode)
	}
	if strings.TrimRight(doc.Issuer, "/") != issuer || !strings.HasPrefix(doc.JWKSURI, "https://") {
		return nil, fmt.Errorf("federation: discovery of %s is inconsistent", issuer)
	}
	k, err = keyfunc.Get(doc.JWKSURI, keyfunc.Options{
		Client: i.Client, RefreshInterval: time.Hour, RefreshRateLimit: time.Minute,
		RefreshTimeout: 10 * time.Second, RefreshUnknownKID: true,
	})
	if err != nil {
		return nil, fmt.Errorf("federation: jwks: %w", err)
	}
	i.mu.Lock()
	i.jwks[issuer] = k
	i.mu.Unlock()
	return k.Keyfunc, nil
}

// ExchangeFederated : vérifie le jeton externe et émet un jeton de 15 min
// pour le compte de service qui lui fait confiance.
func (s *Service) ExchangeFederated(ctx context.Context, in ExchangeRequest) (*Token, iam.Principal, error) {
	switch in.SubjectTokenType {
	case TokenTypeJWT, TokenTypeIDToken, TokenTypeAccess:
	case "":
		return nil, "", &GrantError{Code: "invalid_request", Description: "subject_token_type is required"}
	default:
		return nil, "", &GrantError{Code: "invalid_request", Description: "subject_token_type must be a JWT"}
	}
	if in.SubjectToken == "" {
		return nil, "", &GrantError{Code: "invalid_request", Description: "subject_token is required"}
	}
	if s.Federation == nil {
		return nil, "", &GrantError{Code: "unsupported_grant_type", Description: "federation is not enabled"}
	}

	// 1. Émetteur annoncé : seulement s'il fait l'objet d'une relation de confiance.
	var unverified jwt.RegisteredClaims
	if _, _, err := jwt.NewParser().ParseUnverified(in.SubjectToken, &unverified); err != nil {
		return nil, "", invalidGrant("malformed subject_token")
	}
	issuer, err := iam.NormalizeIssuer(unverified.Issuer)
	if err != nil {
		return nil, "", invalidGrant("no federation trust for this issuer")
	}
	trusts, err := iam.TrustsForIssuer(ctx, s.DB, issuer)
	if err != nil {
		return nil, "", err
	}
	if len(trusts) == 0 {
		return nil, "", invalidGrant("no federation trust for this issuer")
	}

	// 2. Signature et dates, avec les clés de l'émetteur.
	kf, err := s.Federation.keyfunc(ctx, issuer)
	if err != nil {
		return nil, "", err
	}
	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(in.SubjectToken, claims, kf,
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "PS256"}),
		jwt.WithIssuer(unverified.Issuer), jwt.WithAudience(s.Issuer), jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(), jwt.WithLeeway(leeway), jwt.WithTimeFunc(s.now))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenInvalidAudience) {
			return nil, "", invalidGrant(fmt.Sprintf("subject_token audience must be %q", s.Issuer))
		}
		return nil, "", invalidGrant("subject_token: " + describe(err))
	}
	if iat, _ := claims.GetIssuedAt(); iat != nil && s.now().Sub(iat.Time) > maxExternalTokenAge {
		return nil, "", invalidGrant("subject_token is too old")
	}
	subject, _ := claims.GetSubject()

	// 3. Relations satisfaites → compte de service.
	var matched []iam.FederationTrust
	accounts := map[uuid.UUID]bool{}
	for _, t := range trusts {
		if in.ServiceAccount != "" && string(iam.ServiceAccountPrincipal(t.ServiceAccountID)) != in.ServiceAccount {
			continue
		}
		if t.Matches(subject, claims) {
			matched = append(matched, t)
			accounts[t.ServiceAccountID] = true
		}
	}
	switch {
	case len(matched) == 0:
		return nil, "", invalidGrant(fmt.Sprintf("no federation trust matches subject %q", subject))
	case len(accounts) > 1:
		return nil, "", &GrantError{Code: "invalid_request",
			Description: "several service accounts trust this token: pass service_account=serviceAccount:<uuid>"}
	}
	trust := matched[0]
	principal := iam.ServiceAccountPrincipal(trust.ServiceAccountID)

	// 4. Usage unique quand l'émetteur numérote ses jetons.
	if jti, _ := claims["jti"].(string); jti != "" && len(jti) <= maxJTILength {
		exp, _ := claims.GetExpirationTime()
		res := s.DB.WithContext(ctx).Exec(`INSERT INTO iam_assertion_jti (jti, expires_at) VALUES (?, ?)
			ON CONFLICT (jti) DO NOTHING`, "fed:"+issuer+":"+jti, exp.Time.Add(leeway))
		if res.Error != nil {
			return nil, principal, res.Error
		}
		if res.RowsAffected == 0 {
			return nil, principal, invalidGrant("subject_token already used (jti replay)")
		}
	}

	token, exp, err := s.Mint(principal, MethodFederation, trustRefPrefix+trust.ID.String(), FederatedTokenTTL)
	if err != nil {
		return nil, principal, err
	}
	now := s.now()
	_ = s.DB.WithContext(ctx).Exec(`UPDATE iam_federation_trusts SET last_used_at = ? WHERE id = ?`, now, trust.ID).Error
	audit.Annotate(ctx, "federation_trust", trust.ID.String())
	audit.Annotate(ctx, "external_issuer", issuer)
	audit.Annotate(ctx, "external_subject", subject)
	audit.Annotate(ctx, "token_expires_at", exp)
	return &Token{AccessToken: token, TokenType: "Bearer", ExpiresIn: int(exp.Sub(now).Seconds()),
		IssuedTokenType: TokenTypeAccess}, principal, nil
}

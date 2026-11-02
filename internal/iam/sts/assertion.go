package sts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/iam"
)

// Échange d'une assertion signée par une clé de compte de service contre un
// jeton d'accès (RFC 7523, grant jwt-bearer).
//
// L'assertion : en-tête kid = identifiant de la clé, alg = ES256 ou RS256
// (celui de la clé) ; iss = sub = "serviceAccount:<uuid>" ; aud = endpoint de
// jeton ; iat, exp (au plus 1 h après iat) et jti obligatoires. Un jti ne
// sert qu'une fois.

const (
	GrantJWTBearer = "urn:ietf:params:oauth:grant-type:jwt-bearer"

	maxAssertionLifetime = time.Hour
	maxJTILength         = 200
)

// GrantError : refus au format OAuth (RFC 6749 §5.2).
type GrantError struct {
	Code        string // invalid_request | invalid_grant | unsupported_grant_type
	Description string
}

func (e *GrantError) Error() string { return e.Code + ": " + e.Description }

func invalidGrant(desc string) error { return &GrantError{Code: "invalid_grant", Description: desc} }

// Token : réponse du endpoint de jeton.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	// IssuedTokenType : exigé par RFC 8693 pour un token-exchange.
	IssuedTokenType string `json:"issued_token_type,omitempty"`
}

type assertionKey struct {
	SAID        uuid.UUID
	Algorithm   string
	PublicPEM   string
	ValidAfter  time.Time
	ValidBefore time.Time
	Disabled    bool
	SADisabled  bool
}

// ExchangeAssertion : vérifie l'assertion et émet un jeton pour le compte.
// Le principal est renvoyé dès que la clé est identifiée, même en cas de
// refus, pour le journal d'audit.
func (s *Service) ExchangeAssertion(ctx context.Context, assertion string) (*Token, iam.Principal, error) {
	tok, key, err := s.exchangeAssertion(ctx, assertion)
	if key == nil {
		return tok, "", err
	}
	return tok, iam.ServiceAccountPrincipal(key.SAID), err
}

func (s *Service) exchangeAssertion(ctx context.Context, assertion string) (*Token, *assertionKey, error) {
	if assertion == "" {
		return nil, nil, &GrantError{Code: "invalid_request", Description: "assertion is required"}
	}
	var (
		key       *assertionKey
		keyID     string
		lookupErr error
	)
	var c jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(assertion, &c, func(t *jwt.Token) (any, error) {
		keyID, _ = t.Header["kid"].(string)
		k, err := s.lookupKey(ctx, keyID)
		if err != nil {
			lookupErr = err
			return nil, err
		}
		key = k
		if t.Method.Alg() != k.Algorithm {
			return nil, invalidGrant("assertion algorithm does not match the key")
		}
		pub, _, err := iam.ParsePublicKeyPEM(k.PublicPEM)
		if err != nil {
			return nil, err
		}
		return pub, nil
	},
		jwt.WithValidMethods([]string{iam.SAKeyAlgES256, iam.SAKeyAlgRS256}), jwt.WithAudience(s.TokenURI()),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(leeway), jwt.WithTimeFunc(s.now))
	if err != nil {
		var ge *GrantError
		switch {
		case errors.As(lookupErr, &ge):
			return nil, key, ge
		case lookupErr != nil:
			return nil, key, lookupErr // base indisponible : erreur serveur, pas un refus
		case errors.As(err, &ge):
			return nil, key, ge
		}
		return nil, key, invalidGrant(describe(err))
	}

	now := s.now()
	principal := iam.ServiceAccountPrincipal(key.SAID)
	switch {
	case key.Disabled || key.SADisabled:
		return nil, key, invalidGrant("the key or its service account is disabled")
	case now.Before(key.ValidAfter) || !now.Before(key.ValidBefore):
		return nil, key, invalidGrant("the key is expired")
	case c.Issuer != string(principal):
		return nil, key, invalidGrant(fmt.Sprintf("iss must be %q", principal))
	case c.Subject != "" && c.Subject != c.Issuer:
		return nil, key, invalidGrant("sub must equal iss")
	case c.IssuedAt == nil:
		return nil, key, invalidGrant("iat is required")
	case c.ExpiresAt.Sub(c.IssuedAt.Time) > maxAssertionLifetime:
		return nil, key, invalidGrant("assertion lifetime (exp - iat) must not exceed 1 hour")
	case c.ID == "" || len(c.ID) > maxJTILength:
		return nil, key, invalidGrant("jti is required (at most 200 characters)")
	}

	// Anti-rejeu : le jti est réservé jusqu'à l'expiration de l'assertion.
	res := s.DB.WithContext(ctx).Exec(`INSERT INTO iam_assertion_jti (jti, expires_at) VALUES (?, ?)
		ON CONFLICT (jti) DO NOTHING`, keyID+":"+c.ID, c.ExpiresAt.Time.Add(leeway))
	if res.Error != nil {
		return nil, key, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, key, invalidGrant("assertion already used (jti replay)")
	}

	// Le jeton ne survit pas à la clé.
	ttl := MaxTokenTTL
	if left := key.ValidBefore.Sub(now); left < ttl {
		ttl = left
	}
	token, exp, err := s.Mint(principal, MethodJWTBearer, keyID, ttl)
	if err != nil {
		return nil, key, err
	}
	_ = s.DB.WithContext(ctx).Exec(`UPDATE service_account_keys SET last_used_at = ? WHERE key_id = ?`, now, keyID).Error
	audit.Annotate(ctx, "principal", string(principal))
	audit.Annotate(ctx, "service_account_key_id", keyID)
	audit.Annotate(ctx, "token_expires_at", exp)
	return &Token{AccessToken: token, TokenType: "Bearer", ExpiresIn: int(exp.Sub(now).Seconds())}, key, nil
}

func (s *Service) lookupKey(ctx context.Context, keyID string) (*assertionKey, error) {
	if keyID == "" || len(keyID) > 40 {
		return nil, invalidGrant("kid header is required")
	}
	var k assertionKey
	var disabledAt, deletedAt sql.NullTime
	err := s.DB.WithContext(ctx).Raw(`
		SELECT k.service_account_id, k.algorithm, k.public_key_pem, k.valid_after, k.valid_before,
		       k.disabled_at, sa.disabled, sa.deleted_at
		FROM service_account_keys k JOIN service_accounts sa ON sa.id = k.service_account_id
		WHERE k.key_id = ?`, keyID).Row().
		Scan(&k.SAID, &k.Algorithm, &k.PublicPEM, &k.ValidAfter, &k.ValidBefore, &disabledAt, &k.SADisabled, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalidGrant("unknown key")
	}
	if err != nil {
		return nil, err
	}
	k.Disabled = disabledAt.Valid
	k.SADisabled = k.SADisabled || deletedAt.Valid
	return &k, nil
}

// PurgeUsedAssertions : supprime les jti expirés (appelé périodiquement).
func (s *Service) PurgeUsedAssertions(ctx context.Context) error {
	return s.DB.WithContext(ctx).Exec(`DELETE FROM iam_assertion_jti WHERE expires_at < ?`, s.now()).Error
}

func describe(err error) string {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return "assertion expired"
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return "aud must be the token endpoint"
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return "invalid assertion signature"
	case errors.Is(err, jwt.ErrTokenUsedBeforeIssued), errors.Is(err, jwt.ErrTokenNotValidYet):
		return "assertion issued in the future"
	case errors.Is(err, jwt.ErrTokenRequiredClaimMissing):
		return "exp is required"
	case errors.Is(err, jwt.ErrTokenMalformed):
		return "malformed assertion"
	}
	return "invalid assertion"
}

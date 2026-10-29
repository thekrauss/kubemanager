package sts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/iam"
)

// Audience : seule audience des jetons émis (l'API betorun).
const Audience = "betorun-api"

const (
	// MethodJWTBearer : jeton obtenu avec une clé de compte de service (RFC 7523).
	MethodJWTBearer = "jwt-bearer"

	leeway = 30 * time.Second
	// statusTTL : un compte ou une clé désactivé cesse d'être accepté au
	// plus tard après ce délai, même avec un jeton encore valide.
	statusTTL = 30 * time.Second
)

var ErrInvalidToken = errors.New("invalid or expired token")

// Claims : contenu d'un jeton émis par le STS.
type Claims struct {
	jwt.RegisteredClaims
	// Method : preuve présentée pour l'obtenir.
	Method string `json:"btr_auth"`
	// KeyID : clé de compte de service utilisée (jwt-bearer).
	KeyID string `json:"btr_key,omitempty"`
}

// Verified : jeton accepté.
type Verified struct {
	Principal iam.Principal
	Method    string
	KeyID     string
	TokenID   string
	ExpiresAt time.Time
}

// Service : émission et vérification des jetons.
type Service struct {
	Issuer string
	Keys   *KeyRing
	DB     *gorm.DB
	now    func() time.Time

	statusMu sync.Mutex
	status   map[string]statusEntry
}

type statusEntry struct {
	ok      bool
	checked time.Time
}

func NewService(issuer string, keys *KeyRing, db *gorm.DB) *Service {
	return &Service{Issuer: strings.TrimRight(issuer, "/"), Keys: keys, DB: db, now: time.Now, status: map[string]statusEntry{}}
}

// TokenURI : endpoint de jeton (audience des assertions).
func (s *Service) TokenURI() string { return s.Issuer + "/token" }

// JWKSURI : clés publiques publiées.
func (s *Service) JWKSURI() string { return s.Issuer + "/.well-known/jwks.json" }

// Mint signe un jeton pour principal, valable ttl (au plus MaxTokenTTL).
func (s *Service) Mint(principal iam.Principal, method, keyID string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 || ttl > MaxTokenTTL {
		ttl = MaxTokenTTL
	}
	key, err := s.Keys.active()
	if err != nil {
		return "", time.Time{}, err
	}
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	exp := now.Add(ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: s.Issuer, Subject: string(principal), Audience: jwt.ClaimStrings{Audience},
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp), ID: hex.EncodeToString(jti),
		},
		Method: method, KeyID: keyID,
	})
	tok.Header["kid"] = key.kid
	tok.Header["typ"] = "at+jwt" // RFC 9068
	signed, err := tok.SignedString(key.private)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// Owns : le jeton (non vérifié) annonce-t-il ce STS comme émetteur ? Sert à
// aiguiller entre Keycloak (humains) et le STS (machines).
func (s *Service) Owns(raw string) bool {
	var c jwt.RegisteredClaims
	_, _, err := jwt.NewParser().ParseUnverified(raw, &c)
	return err == nil && c.Issuer == s.Issuer
}

// Verify : signature, émetteur, audience, dates, puis état courant du compte
// et de la clé (un compte désactivé ou supprimé n'agit plus).
func (s *Service) Verify(ctx context.Context, raw string) (*Verified, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		pub, ok := s.Keys.publicKey(ctx, kid)
		if !ok {
			return nil, ErrInvalidToken
		}
		return pub, nil
	},
		jwt.WithValidMethods([]string{Algorithm}), jwt.WithIssuer(s.Issuer), jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(leeway), jwt.WithTimeFunc(s.now))
	if err != nil {
		return nil, ErrInvalidToken
	}
	principal := iam.Principal(c.Subject)
	ok, err := s.stillValid(ctx, principal, c.KeyID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidToken
	}
	return &Verified{Principal: principal, Method: c.Method, KeyID: c.KeyID, TokenID: c.ID, ExpiresAt: c.ExpiresAt.Time}, nil
}

// stillValid : compte actif et, le cas échéant, clé non désactivée (cache court).
func (s *Service) stillValid(ctx context.Context, p iam.Principal, keyID string) (bool, error) {
	cacheKey := string(p) + "|" + keyID
	now := s.now()
	s.statusMu.Lock()
	if e, ok := s.status[cacheKey]; ok && now.Sub(e.checked) < statusTTL {
		s.statusMu.Unlock()
		return e.ok, nil
	}
	s.statusMu.Unlock()

	ok, err := s.checkStatus(ctx, p, keyID)
	if err != nil {
		return false, err
	}
	s.statusMu.Lock()
	if len(s.status) > 100_000 { // borne : jamais de croissance sans fin
		s.status = map[string]statusEntry{}
	}
	s.status[cacheKey] = statusEntry{ok: ok, checked: now}
	s.statusMu.Unlock()
	return ok, nil
}

func (s *Service) checkStatus(ctx context.Context, p iam.Principal, keyID string) (bool, error) {
	kind, id, err := p.Parse()
	if err != nil || kind != iam.KindServiceAccount {
		return false, nil
	}
	db := s.DB.WithContext(ctx)
	var disabled bool
	err = db.Raw(`SELECT disabled FROM service_accounts WHERE id = ? AND deleted_at IS NULL`, id).Row().Scan(&disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || disabled {
		return false, err
	}
	if keyID == "" {
		return true, nil
	}
	var keyOK bool
	// Désactivée, ou arrivée à échéance (fin de la grâce d'une rotation).
	err = db.Raw(`SELECT disabled_at IS NULL AND valid_before > ? FROM service_account_keys
		WHERE key_id = ? AND service_account_id = ?`, s.now(), keyID, id).Row().Scan(&keyOK)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return keyOK, err
}

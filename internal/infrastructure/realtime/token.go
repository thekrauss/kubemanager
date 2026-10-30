package realtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thekrauss/kubemanager/internal/core/configs"
)

//go:generate mockgen -source=token.go -destination=mocks/mock_token_signer.go -package=mocks
type TokenSigner interface {
	GenerateConnectionToken(ctx context.Context, subject string, expiresAt time.Time, info map[string]any) (string, error)
	GenerateSubscriptionToken(ctx context.Context, subject string, channel string, expiresAt time.Time) (string, error)
}

type hmacTokenSigner struct {
	enabled bool
	secret  []byte
}

func NewTokenSigner(cfg configs.CentrifugoConfig) (TokenSigner, error) {
	if !cfg.Enabled {
		return noopTokenSigner{}, nil
	}
	secret := strings.TrimSpace(cfg.TokenHMACSecret)
	if secret == "" {
		return nil, fmt.Errorf("centrifugo token_hmac_secret is required when realtime is enabled")
	}
	return &hmacTokenSigner{
		enabled: true,
		secret:  []byte(secret),
	}, nil
}

func (s *hmacTokenSigner) GenerateConnectionToken(_ context.Context, subject string, expiresAt time.Time, info map[string]any) (string, error) {
	if s == nil || !s.enabled {
		return "", nil
	}
	claims := map[string]any{
		"sub": strings.TrimSpace(subject),
		"exp": expiresAt.UTC().Unix(),
	}
	if len(info) > 0 {
		claims["info"] = info
	}
	return s.sign(claims)
}

func (s *hmacTokenSigner) GenerateSubscriptionToken(_ context.Context, subject string, channel string, expiresAt time.Time) (string, error) {
	if s == nil || !s.enabled {
		return "", nil
	}
	claims := map[string]any{
		"sub":     strings.TrimSpace(subject),
		"channel": strings.TrimSpace(channel),
		"exp":     expiresAt.UTC().Unix(),
	}
	return s.sign(claims)
}

func (s *hmacTokenSigner) sign(claims map[string]any) (string, error) {
	headerSegment, err := encodeJWTPart(map[string]any{
		"alg": tokenAlg,
		"typ": tokenTyp,
	})
	if err != nil {
		return "", err
	}
	claimsSegment, err := encodeJWTPart(claims)
	if err != nil {
		return "", err
	}
	unsigned := headerSegment + "." + claimsSegment
	mac := hmac.New(sha256.New, s.secret)
	if _, err := mac.Write([]byte(unsigned)); err != nil {
		return "", err
	}
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return unsigned + "." + signature, nil
}

func encodeJWTPart(v any) (string, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

type noopTokenSigner struct{}

func (noopTokenSigner) GenerateConnectionToken(context.Context, string, time.Time, map[string]any) (string, error) {
	return "", nil
}

func (noopTokenSigner) GenerateSubscriptionToken(context.Context, string, string, time.Time) (string, error) {
	return "", nil
}

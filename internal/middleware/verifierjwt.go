package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/MicahParks/keyfunc/v2"
	"github.com/golang-jwt/jwt/v5"
	authd "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"go.uber.org/zap"
)

type RealmAccess struct {
	Roles []string `json:"roles"`
}

type Claims struct {
	Sub               string      `json:"sub"`
	Scope             string      `json:"scope"`
	TenantID          string      `json:"tenant_id"`
	LegacyTenantID    string      `json:"id_tenant"`
	Roles             []string    `json:"roles"`
	Permissions       []string    `json:"permissions"`
	RealmAccess       RealmAccess `json:"realm_access"`
	PreferredUsername string      `json:"preferred_username"`
	Email             string      `json:"email"`
	// Acr : niveau d'authentification atteint (mapping ACR → LoA du realm Keycloak).
	Acr string `json:"acr"`
	// AuthTime : instant (Unix) de la dernière authentification interactive.
	AuthTime int64 `json:"auth_time"`
	jwt.RegisteredClaims
}

type Config struct {
	JWKSURL  string
	Issuer   string
	Audience string
}

type Verifier struct {
	keyFunc  *keyfunc.JWKS
	logger   *zap.SugaredLogger
	issuer   string
	audience string
}

func NewVerifier(cfg Config, logger *zap.SugaredLogger) (*Verifier, error) {
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	jwksURL := strings.TrimSpace(cfg.JWKSURL)
	if jwksURL == "" {
		return nil, fmt.Errorf("jwks url is required")
	}

	options := keyfunc.Options{
		RefreshTimeout:   jwksRefreshTimeout,
		RefreshInterval:  jwksRefreshInterval,
		RefreshRateLimit: jwksRefreshRateLimit,
	}

	jwks, err := keyfunc.Get(jwksURL, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get JWKS from %s: %w", jwksURL, err)
	}
	logger.Infow("JWKS verifier initialized and keys loaded", "url", jwksURL, "issuer", cfg.Issuer, "audience", cfg.Audience)
	return &Verifier{
		keyFunc:  jwks,
		logger:   logger,
		issuer:   strings.TrimSpace(cfg.Issuer),
		audience: strings.TrimSpace(cfg.Audience),
	}, nil
}

func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	var claims Claims

	parseOptions := []jwt.ParserOption{}
	if v.issuer != "" {
		parseOptions = append(parseOptions, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		parseOptions = append(parseOptions, jwt.WithAudience(v.audience))
	}

	token, err := jwt.ParseWithClaims(tokenString, &claims, v.keyFunc.Keyfunc, parseOptions...)
	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("token is invalid")
	}

	claims.normalize()
	return &claims, nil
}

func (c *Claims) normalize() {
	if strings.TrimSpace(c.TenantID) == "" {
		c.TenantID = strings.TrimSpace(c.LegacyTenantID)
	}

	roles := make([]string, 0, len(c.Roles)+len(c.RealmAccess.Roles))
	roles = append(roles, c.Roles...)
	roles = append(roles, c.RealmAccess.Roles...)
	c.Roles = dedupeNonEmpty(roles)

	if len(c.Permissions) == 0 {
		c.Permissions = permissionsForRoles(c.Roles)
	} else {
		c.Permissions = dedupeNonEmpty(c.Permissions)
	}
}

func (c *Claims) Normalize() {
	if c == nil {
		return
	}
	c.normalize()
}

func (c *Claims) UnmarshalJSON(data []byte) error {
	type alias Claims
	aux := struct {
		alias
		TenantIDAttribute any `json:"tenant_id"`
		LegacyTenantID    any `json:"id_tenant"`
	}{
		alias: alias(*c),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*c = Claims(aux.alias)
	c.TenantID = stringifyClaim(aux.TenantIDAttribute)
	c.LegacyTenantID = stringifyClaim(aux.LegacyTenantID)
	return nil
}

func stringifyClaim(v any) string {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for _, item := range value {
			if s := stringifyClaim(item); s != "" {
				return s
			}
		}
	}
	return ""
}

func permissionsForRoles(roles []string) []string {
	seenPerms := map[string]struct{}{}
	var perms []string

	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}

		roleType, err := authd.RoleTypes.NewFromString(context.Background(), role)
		if err != nil {
			continue
		}

		for _, perm := range authd.RolePermissions(roleType) {
			permStr := perm.String()
			if _, ok := seenPerms[permStr]; ok {
				continue
			}
			seenPerms[permStr] = struct{}{}
			perms = append(perms, permStr)
		}
	}

	slices.Sort(perms)
	return perms
}

func dedupeNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

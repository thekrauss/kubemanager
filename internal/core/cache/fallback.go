package cache

import (
	"context"
	"time"
)

type FallbackAuthCache struct {
	primary  AuthCache
	fallback AuthCache
}

func NewFallbackAuthCache(primary AuthCache, fallback AuthCache) *FallbackAuthCache {
	return &FallbackAuthCache{
		primary:  primary,
		fallback: fallback,
	}
}

func (c *FallbackAuthCache) SetAccessTokenClaims(ctx context.Context, token string, claims AccessTokenClaims, ttl time.Duration) error {
	if err := c.primary.SetAccessTokenClaims(ctx, token, claims, ttl); err != nil {
		return c.fallback.SetAccessTokenClaims(ctx, token, claims, ttl)
	}
	return nil
}

func (c *FallbackAuthCache) GetAccessTokenClaims(ctx context.Context, token string) (AccessTokenClaims, bool, error) {
	v, ok, err := c.primary.GetAccessTokenClaims(ctx, token)
	if err != nil {
		return c.fallback.GetAccessTokenClaims(ctx, token)
	}
	if ok {
		return v, true, nil
	}
	return c.fallback.GetAccessTokenClaims(ctx, token)
}

func (c *FallbackAuthCache) BlacklistAccessTokenJTI(ctx context.Context, jti string, ttl time.Duration) error {
	if err := c.primary.BlacklistAccessTokenJTI(ctx, jti, ttl); err != nil {
		return c.fallback.BlacklistAccessTokenJTI(ctx, jti, ttl)
	}
	_ = c.fallback.BlacklistAccessTokenJTI(ctx, jti, ttl)
	return nil
}

func (c *FallbackAuthCache) IsAccessTokenJTIBlacklisted(ctx context.Context, jti string) (bool, error) {
	blocked, err := c.primary.IsAccessTokenJTIBlacklisted(ctx, jti)
	if err != nil {
		return c.fallback.IsAccessTokenJTIBlacklisted(ctx, jti)
	}
	if blocked {
		return true, nil
	}
	return c.fallback.IsAccessTokenJTIBlacklisted(ctx, jti)
}

func (c *FallbackAuthCache) SetAPIKeyValidation(ctx context.Context, rawKey string, validation APIKeyValidation, ttl time.Duration) error {
	if err := c.primary.SetAPIKeyValidation(ctx, rawKey, validation, ttl); err != nil {
		return c.fallback.SetAPIKeyValidation(ctx, rawKey, validation, ttl)
	}
	return nil
}

func (c *FallbackAuthCache) GetAPIKeyValidation(ctx context.Context, rawKey string) (APIKeyValidation, bool, error) {
	v, ok, err := c.primary.GetAPIKeyValidation(ctx, rawKey)
	if err != nil {
		return c.fallback.GetAPIKeyValidation(ctx, rawKey)
	}
	if ok {
		return v, true, nil
	}
	return c.fallback.GetAPIKeyValidation(ctx, rawKey)
}

func (c *FallbackAuthCache) RevokeAPIKeyPrefix(ctx context.Context, prefix string, ttl time.Duration) error {
	if err := c.primary.RevokeAPIKeyPrefix(ctx, prefix, ttl); err != nil {
		return c.fallback.RevokeAPIKeyPrefix(ctx, prefix, ttl)
	}
	_ = c.fallback.RevokeAPIKeyPrefix(ctx, prefix, ttl)
	return nil
}

func (c *FallbackAuthCache) IsAPIKeyPrefixRevoked(ctx context.Context, prefix string) (bool, error) {
	revoked, err := c.primary.IsAPIKeyPrefixRevoked(ctx, prefix)
	if err != nil {
		return c.fallback.IsAPIKeyPrefixRevoked(ctx, prefix)
	}
	if revoked {
		return true, nil
	}
	return c.fallback.IsAPIKeyPrefixRevoked(ctx, prefix)
}

func (c *FallbackAuthCache) Get(ctx context.Context, key string) (string, error) {
	val, err := c.primary.Get(ctx, key)
	if err != nil || val == "" {
		return c.fallback.Get(ctx, key)
	}
	return val, nil
}

func (c *FallbackAuthCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	if err := c.primary.Set(ctx, key, value, ttl); err != nil {
		return c.fallback.Set(ctx, key, value, ttl)
	}
	return nil
}

func (c *FallbackAuthCache) Delete(ctx context.Context, key string) error {
	_ = c.primary.Delete(ctx, key)
	return c.fallback.Delete(ctx, key)
}

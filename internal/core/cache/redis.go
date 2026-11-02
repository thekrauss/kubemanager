package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisAccessClaimsPrefix = "auth:access:claims:"
	redisBlacklistPrefix    = "auth:access:blacklist:"
	redisAPIKeyPrefix       = "auth:apikey:validation:"
	redisAPIKeyRevokePrefix = "auth:apikey:revoked:"
)

type RedisAuthCache struct {
	client *redis.Client
}

var _ AuthCache = (*RedisAuthCache)(nil)

func NewRedisAuthCache(ctx context.Context, addr string, password string, db int) (*RedisAuthCache, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis address is required")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	pingCtx := ctx
	if pingCtx == nil {
		pingCtx = context.Background()
	}
	if err := client.Ping(pingCtx).Err(); err != nil {
		return nil, err
	}

	return &RedisAuthCache{client: client}, nil
}

func (c *RedisAuthCache) SetAccessTokenClaims(ctx context.Context, token string, claims AccessTokenClaims, ttl time.Duration) error {
	payload, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	key := redisAccessClaimsPrefix + secureKey(token)
	return c.client.Set(ctx, key, payload, ttl).Err()
}

func (c *RedisAuthCache) GetAccessTokenClaims(ctx context.Context, token string) (AccessTokenClaims, bool, error) {
	key := redisAccessClaimsPrefix + secureKey(token)
	raw, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return AccessTokenClaims{}, false, nil
	}
	if err != nil {
		return AccessTokenClaims{}, false, err
	}

	var claims AccessTokenClaims
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		return AccessTokenClaims{}, false, err
	}
	return claims, true, nil
}

func (c *RedisAuthCache) BlacklistAccessTokenJTI(ctx context.Context, jti string, ttl time.Duration) error {
	key := redisBlacklistPrefix + secureKey(jti)
	return c.client.Set(ctx, key, "1", ttl).Err()
}

func (c *RedisAuthCache) IsAccessTokenJTIBlacklisted(ctx context.Context, jti string) (bool, error) {
	key := redisBlacklistPrefix + secureKey(jti)
	n, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (c *RedisAuthCache) SetAPIKeyValidation(ctx context.Context, rawKey string, validation APIKeyValidation, ttl time.Duration) error {
	payload, err := json.Marshal(validation)
	if err != nil {
		return err
	}
	key := redisAPIKeyPrefix + secureKey(rawKey)
	return c.client.Set(ctx, key, payload, ttl).Err()
}

func (c *RedisAuthCache) GetAPIKeyValidation(ctx context.Context, rawKey string) (APIKeyValidation, bool, error) {
	key := redisAPIKeyPrefix + secureKey(rawKey)
	raw, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return APIKeyValidation{}, false, nil
	}
	if err != nil {
		return APIKeyValidation{}, false, err
	}

	var validation APIKeyValidation
	if err := json.Unmarshal([]byte(raw), &validation); err != nil {
		return APIKeyValidation{}, false, err
	}
	return validation, true, nil
}

func (c *RedisAuthCache) RevokeAPIKeyPrefix(ctx context.Context, prefix string, ttl time.Duration) error {
	key := redisAPIKeyRevokePrefix + secureKey(prefix)
	return c.client.Set(ctx, key, "1", ttl).Err()
}

func (c *RedisAuthCache) IsAPIKeyPrefixRevoked(ctx context.Context, prefix string) (bool, error) {
	key := redisAPIKeyRevokePrefix + secureKey(prefix)
	n, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (c *RedisAuthCache) Get(ctx context.Context, key string) (string, error) {
	val, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

func (c *RedisAuthCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *RedisAuthCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

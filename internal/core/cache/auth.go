package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
)

var ErrCacheMiss = betoerrors.New(betoerrors.CodeCacheMiss, "cache miss")

const (
	ServiceKeyPrefix = "kubemanager:"
	KeySession       = "session:"
	KeyLoginFail     = "login:fail:"
	KeyLoginLock     = "login:lock:"
	KeyBlacklist     = "blacklist:"
	KeyResetToken    = "reset_token:"
)

type CacheRedis interface {
	IncrementLoginAttempts(ctx context.Context, userID string, duration time.Duration) (int64, error)
	BlockUser(ctx context.Context, userID string, duration time.Duration) error
	IsUserBlocked(ctx context.Context, userID string) (bool, error)

	StoreSession(ctx context.Context, userID string, session SessionData, ttl time.Duration) error
	GetSession(ctx context.Context, userID string) (*SessionData, error)
	DeleteSession(ctx context.Context, userID string) error

	IsTokenBlacklisted(ctx context.Context, jti string) (bool, error)
	BlacklistToken(ctx context.Context, jti string, ttl time.Duration) error

	StoreResetToken(ctx context.Context, token, userID string, ttl time.Duration) error
	GetResetToken(ctx context.Context, token string) (string, error)
	DeleteResetToken(ctx context.Context, token string) error

	Ping(ctx context.Context) error
}

var _ CacheRedis = (*cacheRedis)(nil)

type cacheRedis struct {
	Client *redis.Client
	Logger *zap.SugaredLogger
}

type SessionData struct {
	UserID       string            `json:"user_id"`
	Email        string            `json:"email"`
	GlobalRole   string            `json:"global_role"`
	ProjectRoles map[string]string `json:"project_roles"`

	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func NewcacheRedis(client *redis.Client, logger *zap.SugaredLogger) *cacheRedis {
	return &cacheRedis{
		Client: client,
		Logger: logger.With("component", "cacheRedis"),
	}
}

func (r *cacheRedis) getKey(prefix, id string) string {
	return fmt.Sprintf("%s%s%s", ServiceKeyPrefix, prefix, id)
}

func (r *cacheRedis) IncrementLoginAttempts(ctx context.Context, userID string, duration time.Duration) (int64, error) {
	key := r.getKey(KeyLoginFail, userID)

	count, err := r.Client.Incr(ctx, key).Result()
	if err != nil {
		r.Logger.Errorw("Failed to increment attempt counter", "key", key, "error", err)
		return 0, betoerrors.Wrap(err, betoerrors.CodeCacheError, "failed to increment login attempts")
	}

	if count == 1 {
		if _, err := r.Client.Expire(ctx, key, duration).Result(); err != nil {
			r.Logger.Errorw("Failed to expire attempt counter", "key", key, "error", err)
		}
	}
	return count, nil
}

func (r *cacheRedis) BlockUser(ctx context.Context, userID string, duration time.Duration) error {
	key := r.getKey(KeyLoginLock, userID)

	if err := r.Client.Set(ctx, key, "1", duration).Err(); err != nil {
		r.Logger.Errorw("Failed to block user", "userID", userID, "error", err)
		return betoerrors.Wrap(err, betoerrors.CodeCacheError, "failed to set user block key")
	}

	r.Client.Del(ctx, r.getKey(KeyLoginFail, userID))

	r.Logger.Infow("User blocked due to too many failed attempts", "userID", userID)
	return nil
}

func (r *cacheRedis) IsUserBlocked(ctx context.Context, userID string) (bool, error) {
	key := r.getKey(KeyLoginLock, userID)
	exists, err := r.Client.Exists(ctx, key).Result()
	if err != nil {
		return false, betoerrors.Wrap(err, betoerrors.CodeCacheError, "failed to check block status")
	}
	return exists > 0, nil
}

func (r *cacheRedis) StoreSession(ctx context.Context, userID string, session SessionData, ttl time.Duration) error {
	key := r.getKey(KeySession, userID)
	return r.setJSON(ctx, key, session, ttl)
}

func (r *cacheRedis) GetSession(ctx context.Context, userID string) (*SessionData, error) {
	key := r.getKey(KeySession, userID)
	var session SessionData
	if err := r.getJSON(ctx, key, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *cacheRedis) DeleteSession(ctx context.Context, userID string) error {
	key := r.getKey(KeySession, userID)
	return r.delete(ctx, key)
}

func (r *cacheRedis) IsTokenBlacklisted(ctx context.Context, jti string) (bool, error) {
	key := r.getKey(KeyBlacklist, jti)
	exists, err := r.Client.Exists(ctx, key).Result()
	return exists > 0, err
}

func (r *cacheRedis) BlacklistToken(ctx context.Context, jti string, ttl time.Duration) error {
	key := r.getKey(KeyBlacklist, jti)
	return r.Client.Set(ctx, key, "1", ttl).Err()
}

func (r *cacheRedis) StoreResetToken(ctx context.Context, token, userID string, ttl time.Duration) error {
	key := r.getKey(KeyResetToken, token)
	return r.Client.Set(ctx, key, userID, ttl).Err()
}

func (r *cacheRedis) GetResetToken(ctx context.Context, token string) (string, error) {
	key := r.getKey(KeyResetToken, token)
	val, err := r.Client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", ErrCacheMiss
	}
	return val, err
}

func (r *cacheRedis) DeleteResetToken(ctx context.Context, token string) error {
	key := r.getKey(KeyResetToken, token)
	return r.delete(ctx, key)
}

func (r *cacheRedis) setJSON(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return betoerrors.Wrap(err, betoerrors.CodeCacheError, "failed to marshal value")
	}
	return r.Client.Set(ctx, key, data, ttl).Err()
}

func (r *cacheRedis) getJSON(ctx context.Context, key string, v interface{}) error {
	data, err := r.Client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return ErrCacheMiss
		}
		return betoerrors.Wrap(err, betoerrors.CodeCacheError, "failed to get value")
	}
	return json.Unmarshal(data, v)
}

func (r *cacheRedis) delete(ctx context.Context, key string) error {
	return r.Client.Del(ctx, key).Err()
}

func (r *cacheRedis) Ping(ctx context.Context) error {
	return r.Client.Ping(ctx).Err()
}

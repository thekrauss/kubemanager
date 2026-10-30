package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type AccessTokenClaims struct {
	UserID    string
	Email     string
	TenantID  string
	Roles     []string
	Scopes    []string
	ExpiresAt int64
	JTI       string
	// Acr et AuthTime : niveau et instant de l'authentification (MFA, step-up).
	Acr      string
	AuthTime int64
}

type APIKeyValidation struct {
	// ServiceAccountID : si renseigné, la clé agit comme ce compte de service
	// (clé legacy rattachée, iam.md §8.4) et non comme UserID.
	ServiceAccountID string
	UserID           string
	TenantID         string
	Kind             string
	Scopes           []string
}

type AuthCache interface {
	SetAccessTokenClaims(ctx context.Context, token string, claims AccessTokenClaims, ttl time.Duration) error
	GetAccessTokenClaims(ctx context.Context, token string) (AccessTokenClaims, bool, error)
	BlacklistAccessTokenJTI(ctx context.Context, jti string, ttl time.Duration) error
	IsAccessTokenJTIBlacklisted(ctx context.Context, jti string) (bool, error)

	SetAPIKeyValidation(ctx context.Context, rawKey string, validation APIKeyValidation, ttl time.Duration) error
	GetAPIKeyValidation(ctx context.Context, rawKey string) (APIKeyValidation, bool, error)
	RevokeAPIKeyPrefix(ctx context.Context, prefix string, ttl time.Duration) error
	IsAPIKeyPrefixRevoked(ctx context.Context, prefix string) (bool, error)

	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

type RistrettoAuthCache struct {
	cache *ristretto.Cache
}

func NewRistrettoAuthCache() (*RistrettoAuthCache, error) {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: ristrettoNumCounters,
		MaxCost:     ristrettoMaxCost,
		BufferItems: ristrettoBufferItems,
	})
	if err != nil {
		return nil, err
	}
	return &RistrettoAuthCache{cache: cache}, nil
}

func (c *RistrettoAuthCache) SetAccessTokenClaims(ctx context.Context, token string, claims AccessTokenClaims, ttl time.Duration) error {
	key := localKeyClaims + secureKey(token)
	c.cache.SetWithTTL(key, claims, 1, ttl)
	return nil
}

func (c *RistrettoAuthCache) GetAccessTokenClaims(ctx context.Context, token string) (AccessTokenClaims, bool, error) {
	key := localKeyClaims + secureKey(token)
	val, ok := c.cache.Get(key)
	if !ok {
		return AccessTokenClaims{}, false, nil
	}
	claims, ok := val.(AccessTokenClaims)
	return claims, ok, nil
}

func (c *RistrettoAuthCache) BlacklistAccessTokenJTI(ctx context.Context, jti string, ttl time.Duration) error {
	key := localKeyJTIBlacklist + secureKey(jti)
	c.cache.SetWithTTL(key, struct{}{}, 1, ttl)
	return nil
}

func (c *RistrettoAuthCache) IsAccessTokenJTIBlacklisted(ctx context.Context, jti string) (bool, error) {
	key := localKeyJTIBlacklist + secureKey(jti)
	_, ok := c.cache.Get(key)
	return ok, nil
}

func (c *RistrettoAuthCache) SetAPIKeyValidation(ctx context.Context, rawKey string, validation APIKeyValidation, ttl time.Duration) error {
	key := localKeyAPIKey + secureKey(rawKey)
	c.cache.SetWithTTL(key, validation, 1, ttl)
	return nil
}

func (c *RistrettoAuthCache) GetAPIKeyValidation(ctx context.Context, rawKey string) (APIKeyValidation, bool, error) {
	key := localKeyAPIKey + secureKey(rawKey)
	val, ok := c.cache.Get(key)
	if !ok {
		return APIKeyValidation{}, false, nil
	}
	v, ok := val.(APIKeyValidation)
	return v, ok, nil
}

func (c *RistrettoAuthCache) RevokeAPIKeyPrefix(ctx context.Context, prefix string, ttl time.Duration) error {
	key := localKeyRevokedKey + secureKey(prefix)
	c.cache.SetWithTTL(key, struct{}{}, 1, ttl)
	return nil
}

func (c *RistrettoAuthCache) IsAPIKeyPrefixRevoked(ctx context.Context, prefix string) (bool, error) {
	key := localKeyRevokedKey + secureKey(prefix)
	_, ok := c.cache.Get(key)
	return ok, nil
}

func (c *RistrettoAuthCache) Get(ctx context.Context, key string) (string, error) {
	val, ok := c.cache.Get(localKeyGeneric + key)
	if !ok {
		return "", nil
	}
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("value is not a string")
	}
	return s, nil
}

func (c *RistrettoAuthCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	c.cache.SetWithTTL(localKeyGeneric+key, value, 1, ttl)
	return nil
}

func (c *RistrettoAuthCache) Delete(ctx context.Context, key string) error {
	c.cache.Del(localKeyGeneric + key)
	return nil
}

func secureKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

var ErrCacheMiss = errors.New("cache miss")

const (
	ServiceKeyPrefix = "kubemanager:"
	KeySession       = "session:"
	KeyLoginFail     = "login:fail:"
	KeyLoginLock     = "login:lock:"
	KeyBlacklist     = "blacklist:"
	KeyResetToken    = "reset_token:"
	KeyUser          = "user:"
)

// Préfixes des clés du cache local (Ristretto) d'authentification.
const (
	localKeyClaims       = "claims:"
	localKeyJTIBlacklist = "bl:jti:"
	localKeyAPIKey       = "ak:"
	localKeyRevokedKey   = "rev:"
	localKeyGeneric      = "gen:"
)

// Dimensionnement du cache local (Ristretto).
const (
	ristrettoNumCounters = 1e7
	ristrettoMaxCost     = 1 << 30
	ristrettoBufferItems = 64
)

type CacheRedis interface {
	SetUser(ctx context.Context, key string, value interface{}, ttl time.Duration) error
	GetUser(ctx context.Context, key string, dest interface{}) error
	DeleteUser(ctx context.Context, key string) error

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

	// Increment : compteur atomique distribué.
	// INCR le compteur à `key`, et sur la première valeur pose un TTL = `window`.
	// Usage : rate limiting (ex : max N créations d'API key / 24h par user).
	// Retourne la nouvelle valeur après incrémentation.
	Increment(ctx context.Context, key string, window time.Duration) (int64, error)

	// SetValue / GetValue / DeleteValue : KV string générique avec TTL.
	// Usage : state CSRF OAuth (one-time, cross-instance).
	SetValue(ctx context.Context, key, value string, ttl time.Duration) error
	GetValue(ctx context.Context, key string) (string, error)
	DeleteValue(ctx context.Context, key string) error

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

func (r *cacheRedis) SetUser(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return r.setJSON(ctx, key, value, ttl)
}

func (r *cacheRedis) GetUser(ctx context.Context, key string, dest interface{}) error {
	return r.getJSON(ctx, key, dest)
}

func (r *cacheRedis) DeleteUser(ctx context.Context, key string) error {
	return r.delete(ctx, key)
}

func (r *cacheRedis) getKey(prefix, id string) string {
	return ServiceKeyPrefix + prefix + id
}

func (r *cacheRedis) IncrementLoginAttempts(ctx context.Context, userID string, duration time.Duration) (int64, error) {
	key := r.getKey(KeyLoginFail, userID)

	count, err := r.Client.Incr(ctx, key).Result()
	if err != nil {
		r.Logger.Errorw("Failed to increment attempt counter", "key", key, "error", err)
		return 0, fmt.Errorf("failed to increment login attempts: %w", err)
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
		return fmt.Errorf("failed to set user block key: %w", err)
	}

	r.Client.Del(ctx, r.getKey(KeyLoginFail, userID))

	r.Logger.Infow("User blocked due to too many failed attempts", "userID", userID)
	return nil
}

func (r *cacheRedis) IsUserBlocked(ctx context.Context, userID string) (bool, error) {
	key := r.getKey(KeyLoginLock, userID)
	exists, err := r.Client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check block status: %w", err)
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
		return fmt.Errorf("failed to marshal value: %w", err)
	}
	return r.Client.Set(ctx, key, data, ttl).Err()
}

func (r *cacheRedis) getJSON(ctx context.Context, key string, v interface{}) error {
	data, err := r.Client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return ErrCacheMiss
		}
		return fmt.Errorf("failed to get value: %w", err)
	}
	return json.Unmarshal(data, v)
}

func (r *cacheRedis) delete(ctx context.Context, key string) error {
	return r.Client.Del(ctx, key).Err()
}

func (r *cacheRedis) Ping(ctx context.Context) error {
	return r.Client.Ping(ctx).Err()
}

// Increment atomic INCR + EXPIRE (sur première incrémentation uniquement).
// Évite les resets de TTL qui permettraient à un attaquant de maintenir le
// compteur sous la limite en jouant avec la fenêtre.
func (r *cacheRedis) Increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	fullKey := ServiceKeyPrefix + key
	count, err := r.Client.Incr(ctx, fullKey).Result()
	if err != nil {
		return 0, fmt.Errorf("redis incr: %w", err)
	}
	if count == 1 && window > 0 {
		if _, expErr := r.Client.Expire(ctx, fullKey, window).Result(); expErr != nil {
			r.Logger.Warnw("failed to set TTL on counter", "key", fullKey, "error", expErr)
		}
	}
	return count, nil
}

func (r *cacheRedis) SetValue(ctx context.Context, key, value string, ttl time.Duration) error {
	fullKey := ServiceKeyPrefix + key
	return r.Client.Set(ctx, fullKey, value, ttl).Err()
}

func (r *cacheRedis) GetValue(ctx context.Context, key string) (string, error) {
	fullKey := ServiceKeyPrefix + key
	val, err := r.Client.Get(ctx, fullKey).Result()
	if err == redis.Nil {
		return "", ErrCacheMiss
	}
	return val, err
}

func (r *cacheRedis) DeleteValue(ctx context.Context, key string) error {
	fullKey := ServiceKeyPrefix + key
	return r.Client.Del(ctx, fullKey).Err()
}

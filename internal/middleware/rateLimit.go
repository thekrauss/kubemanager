package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/thekrauss/kubemanager/internal/core/httpheader"
)

// RateLimitConfig : fixed-window Redis-backed rate limit (per IP).
//
//	Limit   = max requests per Window
//	Burst   = ignored (kept for call-site compat, not meaningful in fixed window)
//	Window  = bucket duration (default 1 minute)
//	Enabled = master switch
type RateLimitConfig struct {
	Enabled bool
	Limit   int
	Burst   int
	Window  time.Duration
}

// Lua : atomic INCR + set TTL on first hit, returns the new counter value.
var rateLimitScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return current
`)

// RateLimitMiddleware enforces a per-IP fixed-window rate limit backed by Redis.
//
// Design (prod-oriented) :
//   - Redis obligatoire quand Enabled=true. Si rdb est nil, la middleware se
//     désactive en bruyant (log stderr) plutôt que de laisser passer du trafic
//     non compté.
//   - Sur erreur Redis runtime : fail-closed (503) — préserver la garantie du
//     rate limit quand on scale à N replicas, mieux vaut refuser que trop
//     laisser passer.
//   - Fenêtre par défaut 1 minute. Clé : `rl:{ip}:{bucket}` expirée par TTL
//     Redis, donc pas de cron d'éviction côté serveur.
func RateLimitMiddleware(cfg RateLimitConfig, rdb *redis.Client) gin.HandlerFunc {
	if !cfg.Enabled || cfg.Limit <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	if rdb == nil {
		fmt.Println("[middleware] RateLimit: redis client nil, disabling (no-op)")
		return func(c *gin.Context) { c.Next() }
	}
	window := cfg.Window
	if window <= 0 {
		window = defaultRateLimitWindow
	}
	windowMillis := strconv.FormatInt(window.Milliseconds(), 10)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		if ip == "" {
			c.Next()
			return
		}

		bucketStart := time.Now().Truncate(window)
		key := fmt.Sprintf(rateLimitKeyFormat, ip, bucketStart.Unix())

		ctx, cancel := context.WithTimeout(c.Request.Context(), rateLimitBackendTimeout)
		defer cancel()

		res, err := rateLimitScript.Run(ctx, rdb, []string{key}, windowMillis).Result()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error":   errCodeRateLimitBackendUnavailable,
				"message": "rate limiter backend unavailable, retry shortly",
			})
			return
		}
		count, _ := res.(int64)
		if count > int64(cfg.Limit) {
			retryAfter := int(time.Until(bucketStart.Add(window)).Seconds()) + 1
			c.Header(httpheader.RetryAfter, strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   errCodeRateLimitExceeded,
				"message": "too many requests, please slow down",
			})
			return
		}
		c.Next()
	}
}

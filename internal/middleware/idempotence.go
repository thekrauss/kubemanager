package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	idempotencyinfra "github.com/thekrauss/kubemanager/internal/infrastructure/idempotence"
)

// Package-level singletons, set once at startup via SetIdempotencyManager.
var (
	globalIdempotencyMgr    idempotencyinfra.AtomicManager
	globalIdempotencyLogger *zap.SugaredLogger
)

// Limites anti-DoS (fix M10) :
//   - maxIdempotencyBodySize : plafond pour io.ReadAll du body requête
//   - maxIdempotencyCacheSize : plafond pour le buffer de réponse cachée
//
// Au-delà, la requête passe normalement mais n'est PAS mise en cache idempotent.
const (
	maxIdempotencyBodySize  = 1 << 20 // 1 MiB
	maxIdempotencyCacheSize = 1 << 20 // 1 MiB
)

// SetIdempotencyManager wires the idempotency manager used by the middleware.
// If mgr does not implement AtomicManager the middleware becomes a no-op.
func SetIdempotencyManager(mgr idempotencyinfra.Manager, logger *zap.SugaredLogger) {
	if atomic, ok := mgr.(idempotencyinfra.AtomicManager); ok {
		globalIdempotencyMgr = atomic
	}
	globalIdempotencyLogger = logger
}

// cachedHTTPResponse is the value stored under each idempotency key.
type cachedHTTPResponse struct {
	StatusCode  int             `json:"status_code"`
	Body        json.RawMessage `json:"body"`
	ContentType string          `json:"content_type"`
}

// captureWriter wrap gin.ResponseWriter et buffère la réponse pour la mettre
// en cache idempotent. Fix M10 : le buffer est plafonné — au-delà,
// `overflowed=true`, le cache est abandonné mais la réponse est toujours
// envoyée au client (pas de double écriture).
type captureWriter struct {
	gin.ResponseWriter
	buf        bytes.Buffer
	status     int
	maxBytes   int
	overflowed bool
}

func (w *captureWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	// Toujours écrire au client d'abord.
	n, err := w.ResponseWriter.Write(b)
	// Buffer uniquement tant qu'on n'a pas dépassé la limite.
	if !w.overflowed {
		if w.buf.Len()+len(b) > w.maxBytes {
			w.overflowed = true
			w.buf.Reset() // libère la mémoire — on n'écrira pas en cache
		} else {
			w.buf.Write(b)
		}
	}
	return n, err
}

func (w *captureWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// Idempotency returns a Gin middleware that enforces request idempotency via
// the "Idempotency-Key" header.
//
// Must be placed AFTER the auth middleware so UserIDFromContext returns a
// value (the scoped key includes the user ID to prevent cross-user replay).
//
// Behaviour:
//   - No header → pass-through (idempotency is opt-in on the client side).
//   - BeginStarted  → capture the response, store on 2xx, call Fail on error.
//   - BeginCompleted → replay the cached response, add "Idempotency-Status: hit".
//   - BeginInProgress → 409 Conflict with Retry-After: 5.
//   - ErrPayloadMismatch → 409 Conflict (same key, different body).
func Idempotency() gin.HandlerFunc {
	return func(c *gin.Context) {
		mgr := globalIdempotencyMgr
		log := globalIdempotencyLogger

		if mgr == nil {
			c.Next()
			return
		}

		key := strings.TrimSpace(c.GetHeader(httpheader.IdempotencyKey))
		if key == "" {
			c.Next()
			return
		}

		// Fix M11 : pas d'userID → anonyme. Skip l'idempotence plutôt que de
		// partager un bucket global (cache poisoning cross-anonymous).
		userID, hasUser := UserIDFromContext(c)
		if !hasUser || userID == "" {
			if log != nil {
				log.Warnw("idempotency: no userID in context, skipping", "path", c.Request.URL.Path)
			}
			c.Next()
			return
		}

		// Fix M10 : plafonne la lecture du body pour éviter OOM sur gros upload.
		// LimitReader + 1 byte de detection pour savoir si on a dépassé.
		limited := io.LimitReader(c.Request.Body, maxIdempotencyBodySize+1)
		bodyBytes, err := io.ReadAll(limited)
		if err != nil {
			c.Next()
			return
		}
		if len(bodyBytes) > maxIdempotencyBodySize {
			// Payload trop gros pour hasher → on passe en pass-through.
			// Reconstruit le body avec ce qu'on a lu + le reste non consommé.
			c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(bodyBytes), c.Request.Body))
			if log != nil {
				log.Warnw("idempotency: body too large, skipping", "limit", maxIdempotencyBodySize)
			}
			c.Next()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Hash = body bytes + HTTP method + full path with query string.
		// Using the full URL so that ?foo=1 and ?foo=2 are treated as different requests.
		hash, err := mgr.Hash(bodyBytes, c.Request.Method, c.Request.URL.RequestURI())
		if err != nil {
			if log != nil {
				log.Warnw("idempotency: hash failed, skipping", "error", err)
			}
			c.Next()
			return
		}

		// Scope la clé par user — un user A ne peut pas replay la réponse de B.
		scopedKey := fmt.Sprintf(idempotencyKeyFormat, userID, c.Request.Method, c.FullPath(), key)

		ctx := c.Request.Context()

		var cached cachedHTTPResponse
		beginStatus, err := mgr.Begin(ctx, scopedKey, hash, idempotencyProcessingTTL, &cached)
		if err != nil {
			switch {
			case errors.Is(err, idempotencyinfra.ErrPayloadMismatch):
				c.AbortWithStatusJSON(http.StatusConflict, gin.H{
					"error":             "Idempotency-Key already used with a different request payload",
					idempotencyErrorKey: idempotencyErrPayloadMismatch,
				})
			case errors.Is(err, idempotencyinfra.ErrInProgress):
				c.Header(httpheader.RetryAfter, idempotencyRetryAfter)
				c.AbortWithStatusJSON(http.StatusConflict, gin.H{
					"error":             "Identical request already being processed — retry in a few seconds",
					idempotencyErrorKey: idempotencyErrInProgress,
				})
			default:
				if log != nil {
					log.Warnw("idempotency: Begin failed, skipping", "key", scopedKey, "error", err)
				}
				c.Next()
			}
			return
		}

		switch beginStatus {
		case idempotencyinfra.BeginCompleted:
			ct := cached.ContentType
			if ct == "" {
				ct = httpheader.MIMEJSONUTF8
			}
			c.Header(httpheader.IdempotencyStatus, idempotencyStatusHit)
			c.Data(cached.StatusCode, ct, cached.Body)
			c.Abort()
			return

		case idempotencyinfra.BeginInProgress:
			c.Header(httpheader.RetryAfter, idempotencyRetryAfter)
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"error":             "Identical request already being processed — retry in a few seconds",
				idempotencyErrorKey: idempotencyErrInProgress,
			})
			return
		}

		// BeginStarted: wrap the writer, run the handler, then decide what to store.
		cw := &captureWriter{
			ResponseWriter: c.Writer,
			status:         http.StatusOK,
			maxBytes:       maxIdempotencyCacheSize, // fix M10 : cap buffer réponse
		}
		c.Writer = cw

		c.Header(httpheader.IdempotencyStatus, idempotencyStatusMiss)
		c.Next()

		// Client parti pendant le traitement : la clé doit tout de même être
		// libérée ou enregistrée, sinon elle resterait bloquée jusqu'à expiration.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), idempotencyFinalizeTimeout)
		defer cancel()

		switch {
		case cw.overflowed:
			// Fix M10 : réponse trop grosse → on n'enrichit pas le cache.
			// On Fail pour que la clé ne reste pas bloquée "in_progress".
			if log != nil {
				log.Warnw("idempotency: response too large, not cached",
					"key", scopedKey, "limit", maxIdempotencyCacheSize)
			}
			if failErr := mgr.Fail(ctx, scopedKey, hash); failErr != nil && log != nil {
				log.Warnw("idempotency: Fail failed", "key", scopedKey, "error", failErr)
			}
		case cw.status >= 200 && cw.status < 300:
			resp := cachedHTTPResponse{
				StatusCode:  cw.status,
				Body:        cw.buf.Bytes(),
				ContentType: cw.Header().Get(httpheader.ContentType),
			}
			if completeErr := mgr.Complete(ctx, scopedKey, hash, resp); completeErr != nil && log != nil {
				log.Warnw("idempotency: Complete failed", "key", scopedKey, "error", completeErr)
			}
		default:
			// Non-2xx: Fail pour que le client puisse retry avec la même clé.
			if failErr := mgr.Fail(ctx, scopedKey, hash); failErr != nil && log != nil {
				log.Warnw("idempotency: Fail failed", "key", scopedKey, "error", failErr)
			}
		}
	}
}

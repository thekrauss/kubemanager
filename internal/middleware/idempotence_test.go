package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	idempotencyinfra "github.com/thekrauss/kubemanager/internal/infrastructure/idempotence"
)

// ── Fake AtomicManager in-memory ─────────────────────────────────────────

type fakeAtomicManager struct {
	mu      sync.Mutex
	records map[string]*fakeRecord
}

type fakeRecord struct {
	hash   string
	status string // "in_progress" | "completed"
	body   []byte
}

func newFakeManager() *fakeAtomicManager {
	return &fakeAtomicManager{records: make(map[string]*fakeRecord)}
}

// Manager interface.
func (f *fakeAtomicManager) Hash(payload any, meta ...string) (string, error) {
	h := sha256.New()
	if b, ok := payload.([]byte); ok {
		h.Write(b)
	} else {
		data, _ := json.Marshal(payload)
		h.Write(data)
	}
	for _, m := range meta {
		h.Write([]byte(m))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (f *fakeAtomicManager) Lookup(ctx context.Context, key, hash string, dst any) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[key]
	if !ok {
		return false, nil
	}
	if rec.hash != hash {
		return false, idempotencyinfra.ErrPayloadMismatch
	}
	if rec.status == "in_progress" {
		return false, idempotencyinfra.ErrInProgress
	}
	if dst != nil && rec.body != nil {
		_ = json.Unmarshal(rec.body, dst)
	}
	return true, nil
}

func (f *fakeAtomicManager) Store(ctx context.Context, key, hash string, resp any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := json.Marshal(resp)
	f.records[key] = &fakeRecord{hash: hash, status: "completed", body: body}
	return nil
}

// AtomicManager interface.
func (f *fakeAtomicManager) Begin(ctx context.Context, key, hash string, ttl time.Duration, dst any) (idempotencyinfra.BeginStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[key]
	if !ok {
		f.records[key] = &fakeRecord{hash: hash, status: "in_progress"}
		return idempotencyinfra.BeginStarted, nil
	}
	if rec.hash != hash {
		return "", idempotencyinfra.ErrPayloadMismatch
	}
	if rec.status == "completed" {
		if dst != nil && rec.body != nil {
			_ = json.Unmarshal(rec.body, dst)
		}
		return idempotencyinfra.BeginCompleted, nil
	}
	return idempotencyinfra.BeginInProgress, nil
}

func (f *fakeAtomicManager) Complete(ctx context.Context, key, hash string, resp any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := json.Marshal(resp)
	f.records[key] = &fakeRecord{hash: hash, status: "completed", body: body}
	return nil
}

func (f *fakeAtomicManager) Fail(ctx context.Context, key, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, key)
	return nil
}

var _ idempotencyinfra.AtomicManager = (*fakeAtomicManager)(nil)

// ── Tests ────────────────────────────────────────────────────────────────

func setupIdempotenceRouter(mgr idempotencyinfra.AtomicManager, hits *int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	SetIdempotencyManager(mgr, nil)

	r := gin.New()
	// Simule un user authentifié (scope de la key d'idempotence).
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	r.Use(Idempotency())
	r.POST("/resource", func(c *gin.Context) {
		*hits++
		c.JSON(http.StatusCreated, gin.H{"count": *hits})
	})
	return r
}

func TestIdempotency_NoHeader_PassThrough(t *testing.T) {
	hits := 0
	r := setupIdempotenceRouter(newFakeManager(), &hits)

	req := httptest.NewRequest(http.MethodPost, "/resource", bytes.NewBufferString(`{"x":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, hits)
	assert.Empty(t, w.Header().Get("Idempotency-Status"))
}

func TestIdempotency_FirstCall_ThenReplay(t *testing.T) {
	hits := 0
	mgr := newFakeManager()
	r := setupIdempotenceRouter(mgr, &hits)

	payload := `{"name":"foo"}`

	// Premier appel : BeginStarted → le handler s'exécute, la réponse est stockée.
	req1 := httptest.NewRequest(http.MethodPost, "/resource", bytes.NewBufferString(payload))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", "abc-123")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	require.Equal(t, http.StatusCreated, w1.Code)
	assert.Equal(t, 1, hits)
	assert.Equal(t, "miss", w1.Header().Get("Idempotency-Status"))

	// Deuxième appel avec la même key + même payload → replay cached.
	req2 := httptest.NewRequest(http.MethodPost, "/resource", bytes.NewBufferString(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "abc-123")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	require.Equal(t, http.StatusCreated, w2.Code)
	assert.Equal(t, 1, hits, "handler ne doit PAS être rappelé — replay cached")
	assert.Equal(t, "hit", w2.Header().Get("Idempotency-Status"))
	assert.Equal(t, w1.Body.String(), w2.Body.String(), "réponses identiques")
}

func TestIdempotency_PayloadMismatch_409(t *testing.T) {
	hits := 0
	mgr := newFakeManager()
	r := setupIdempotenceRouter(mgr, &hits)

	// Premier appel avec payload P1.
	req1 := httptest.NewRequest(http.MethodPost, "/resource", bytes.NewBufferString(`{"x":1}`))
	req1.Header.Set("Idempotency-Key", "same-key")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusCreated, w1.Code)

	// Deuxième appel même key mais payload différent → 409 mismatch.
	req2 := httptest.NewRequest(http.MethodPost, "/resource", bytes.NewBufferString(`{"x":2}`))
	req2.Header.Set("Idempotency-Key", "same-key")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), "payload_mismatch")
}

func TestIdempotency_InProgress_409_WithRetryAfter(t *testing.T) {
	hits := 0
	mgr := newFakeManager()

	// Pré-remplir : key déjà en "in_progress".
	hash, _ := mgr.Hash([]byte(`{"x":1}`), "POST", "/resource")
	mgr.records["idem:user-1:POST:/resource:my-key"] = &fakeRecord{
		hash: hash, status: "in_progress",
	}

	r := setupIdempotenceRouter(mgr, &hits)

	req := httptest.NewRequest(http.MethodPost, "/resource", bytes.NewBufferString(`{"x":1}`))
	req.Header.Set("Idempotency-Key", "my-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "5", w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), "in_progress")
	assert.Equal(t, 0, hits, "handler ne doit pas être appelé")
}

// Fix M11 : pas d'userID → skip idempotence (pas de bucket anonymous partagé).
func TestIdempotency_NoUserID_SkipsIdempotence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := newFakeManager()
	SetIdempotencyManager(mgr, nil)

	hits := 0
	r := gin.New()
	// PAS de middleware qui set userID — on simule un endpoint public.
	r.Use(Idempotency())
	r.POST("/public", func(c *gin.Context) {
		hits++
		c.JSON(http.StatusOK, gin.H{"count": hits})
	})

	// Deux appels avec la même Idempotency-Key.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/public", bytes.NewBufferString(`{"x":1}`))
		req.Header.Set("Idempotency-Key", "k1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}

	// Sans userID, le middleware skip → handler appelé à chaque requête.
	assert.Equal(t, 2, hits, "handler doit être appelé 2× (pas de cache)")

	// Aucun record ne doit avoir été créé.
	mgr.mu.Lock()
	assert.Empty(t, mgr.records, "aucun record idempotent ne doit être créé")
	mgr.mu.Unlock()
}

// Fix M10 : body trop gros → pass-through sans cache (pas d'OOM).
func TestIdempotency_BodyTooLarge_PassThrough(t *testing.T) {
	hits := 0
	mgr := newFakeManager()
	r := setupIdempotenceRouter(mgr, &hits)

	// 2 MiB de payload > maxIdempotencyBodySize (1 MiB).
	bigBody := bytes.Repeat([]byte("x"), 2*1024*1024)

	req := httptest.NewRequest(http.MethodPost, "/resource", bytes.NewBuffer(bigBody))
	req.Header.Set("Idempotency-Key", "big-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// La requête passe normalement, mais rien n'est caché.
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, hits)
	mgr.mu.Lock()
	assert.Empty(t, mgr.records, "body trop gros → pas de record")
	mgr.mu.Unlock()
}

func TestIdempotency_HandlerFailure_MarkedAsFail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := newFakeManager()
	SetIdempotencyManager(mgr, nil)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	r.Use(Idempotency())
	r.POST("/resource", func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "boom"})
	})

	req1 := httptest.NewRequest(http.MethodPost, "/resource", bytes.NewBufferString(`{"x":1}`))
	req1.Header.Set("Idempotency-Key", "fail-key")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusInternalServerError, w1.Code)

	// Non-2xx → Fail() est appelé, donc la key est retirée → retry autorisé.
	scopedKey := fmt.Sprintf("idem:user-1:POST:/resource:fail-key")
	mgr.mu.Lock()
	_, exists := mgr.records[scopedKey]
	mgr.mu.Unlock()
	assert.False(t, exists, "record doit être effacé après échec")
}

package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrPayloadMismatch = errors.New("idempotency payload mismatch")
	ErrInProgress      = errors.New("idempotency request in progress")
)

type Manager interface {
	Lookup(ctx context.Context, key string, payloadHash string, dst any) (bool, error)
	Store(ctx context.Context, key string, payloadHash string, response any) error
	Hash(payload any, meta ...string) (string, error)
}

type BeginStatus string

const (
	BeginStarted    BeginStatus = "started"
	BeginCompleted  BeginStatus = "completed"
	BeginInProgress BeginStatus = "in_progress"
)

type AtomicManager interface {
	Manager
	Begin(ctx context.Context, key string, payloadHash string, processingTTL time.Duration, dst any) (BeginStatus, error)
	Complete(ctx context.Context, key string, payloadHash string, response any) error
	Fail(ctx context.Context, key string, payloadHash string) error
}

type MetricsProvider interface {
	Metrics() MetricsSnapshot
}

type RedisManager struct {
	client *redis.Client
	ttl    time.Duration
	stats  metrics
}

type metrics struct {
	hit            atomic.Uint64
	miss           atomic.Uint64
	inProgress     atomic.Uint64
	mismatch       atomic.Uint64
	completeFailed atomic.Uint64
}

type MetricsSnapshot struct {
	Hit            uint64 `json:"hit"`
	Miss           uint64 `json:"miss"`
	InProgress     uint64 `json:"in_progress"`
	Mismatch       uint64 `json:"mismatch"`
	CompleteFailed uint64 `json:"complete_failed"`
}

func NewRedisManager(opt *redis.Options, ttl time.Duration) *RedisManager {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &RedisManager{
		client: redis.NewClient(opt),
		ttl:    ttl,
	}
}

type record struct {
	Hash   string          `json:"hash"`
	Status string          `json:"status,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

func (m *RedisManager) key(key string) string {
	return fmt.Sprintf("idempotency:%s", key)
}

func (m *RedisManager) Lookup(ctx context.Context, key string, payloadHash string, dst any) (bool, error) {
	if key == "" {
		return false, nil
	}
	rec, err := m.getRecord(ctx, key)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			m.stats.miss.Add(1)
			return false, nil
		}
		return false, err
	}
	if rec.Hash != payloadHash {
		m.stats.mismatch.Add(1)
		return false, ErrPayloadMismatch
	}
	if rec.Status == statusProcessing {
		m.stats.inProgress.Add(1)
		return false, ErrInProgress
	}
	m.stats.hit.Add(1)
	if dst == nil {
		return true, nil
	}
	if len(rec.Body) == 0 {
		return true, nil
	}
	if err := json.Unmarshal(rec.Body, dst); err != nil {
		return false, err
	}
	return true, nil
}

func (m *RedisManager) Store(ctx context.Context, key string, payloadHash string, response any) error {
	err := m.storeCompleted(ctx, key, payloadHash, response, m.ttl)
	if err != nil {
		m.stats.completeFailed.Add(1)
	}
	return err
}

func (m *RedisManager) Begin(ctx context.Context, key string, payloadHash string, processingTTL time.Duration, dst any) (BeginStatus, error) {
	if key == "" {
		return BeginStarted, nil
	}
	if processingTTL <= 0 {
		processingTTL = 2 * time.Minute
	}
	data, err := json.Marshal(record{
		Hash:   payloadHash,
		Status: statusProcessing,
	})
	if err != nil {
		return "", err
	}
	ok, err := m.client.SetNX(ctx, m.key(key), data, processingTTL).Result()
	if err != nil {
		return "", err
	}
	if ok {
		m.stats.miss.Add(1)
		return BeginStarted, nil
	}

	rec, err := m.getRecord(ctx, key)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			ok, retryErr := m.client.SetNX(ctx, m.key(key), data, processingTTL).Result()
			if retryErr != nil {
				return "", retryErr
			}
			if ok {
				m.stats.miss.Add(1)
				return BeginStarted, nil
			}
			rec, err = m.getRecord(ctx, key)
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	if rec.Hash != payloadHash {
		m.stats.mismatch.Add(1)
		return "", ErrPayloadMismatch
	}
	if rec.Status == statusProcessing {
		m.stats.inProgress.Add(1)
		return BeginInProgress, nil
	}
	m.stats.hit.Add(1)
	if dst != nil && len(rec.Body) > 0 {
		if err := json.Unmarshal(rec.Body, dst); err != nil {
			return "", err
		}
	}
	return BeginCompleted, nil
}

func (m *RedisManager) Complete(ctx context.Context, key string, payloadHash string, response any) error {
	err := m.compareAndSwap(ctx, key, payloadHash, func() (*record, error) {
		body, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		return &record{
			Hash:   payloadHash,
			Status: statusCompleted,
			Body:   body,
		}, nil
	})
	if err != nil {
		m.stats.completeFailed.Add(1)
	}
	return err
}

func (m *RedisManager) Fail(ctx context.Context, key string, payloadHash string) error {
	if key == "" {
		return nil
	}
	return m.compareAndSwap(ctx, key, payloadHash, nil)
}

func (m *RedisManager) Hash(payload any, meta ...string) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, _ = hasher.Write(data)
	for _, piece := range meta {
		_, _ = hasher.Write([]byte(piece))
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (m *RedisManager) Metrics() MetricsSnapshot {
	return MetricsSnapshot{
		Hit:            m.stats.hit.Load(),
		Miss:           m.stats.miss.Load(),
		InProgress:     m.stats.inProgress.Load(),
		Mismatch:       m.stats.mismatch.Load(),
		CompleteFailed: m.stats.completeFailed.Load(),
	}
}

const (
	statusProcessing = "processing"
	statusCompleted  = "completed"
)

func (m *RedisManager) getRecord(ctx context.Context, key string) (*record, error) {
	raw, err := m.client.Get(ctx, m.key(key)).Bytes()
	if err != nil {
		return nil, err
	}
	return decodeRecord(raw)
}

func decodeRecord(raw []byte) (*record, error) {
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	if rec.Status == "" {
		rec.Status = statusCompleted
	}
	return &rec, nil
}

func (m *RedisManager) storeCompleted(ctx context.Context, key string, payloadHash string, response any, ttl time.Duration) error {
	if key == "" {
		return nil
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record{
		Hash:   payloadHash,
		Status: statusCompleted,
		Body:   body,
	})
	if err != nil {
		return err
	}
	return m.client.Set(ctx, m.key(key), data, ttl).Err()
}

func (m *RedisManager) compareAndSwap(ctx context.Context, key string, payloadHash string, build func() (*record, error)) error {
	if key == "" {
		return nil
	}
	redisKey := m.key(key)
	for attempt := 0; attempt < 3; attempt++ {
		err := m.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, redisKey).Bytes()
			if err != nil {
				if errors.Is(err, redis.Nil) {
					return nil
				}
				return err
			}
			rec, err := decodeRecord(raw)
			if err != nil {
				return err
			}
			if rec.Hash != payloadHash {
				return ErrPayloadMismatch
			}
			if rec.Status != statusProcessing {
				return nil
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				if build == nil {
					pipe.Del(ctx, redisKey)
					return nil
				}
				next, buildErr := build()
				if buildErr != nil {
					return buildErr
				}
				data, marshalErr := json.Marshal(next)
				if marshalErr != nil {
					return marshalErr
				}
				pipe.Set(ctx, redisKey, data, m.ttl)
				return nil
			})
			return err
		}, redisKey)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
	return redis.TxFailedErr
}

var _ AtomicManager = (*RedisManager)(nil)

package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
)

type fakeBatchRepo struct {
	repository.AuthRepository
	mu       sync.Mutex
	batches  []map[uuid.UUID]int
	flushes  atomic.Uint64
	errOn    int // flush #N retourne erreur (0 = jamais)
	errCount atomic.Int64
}

func (f *fakeBatchRepo) BatchIncrementAPIKeyUsage(ctx context.Context, m map[uuid.UUID]int) error {
	call := int(f.flushes.Add(1))
	if f.errOn > 0 && call == f.errOn {
		f.errCount.Add(1)
		return assert.AnError
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make(map[uuid.UUID]int, len(m))
	for k, v := range m {
		cp[k] = v
	}
	f.batches = append(f.batches, cp)
	return nil
}

func (f *fakeBatchRepo) totalIncrs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := 0
	for _, b := range f.batches {
		for _, v := range b {
			sum += v
		}
	}
	return sum
}

func TestAccumulator_Incr_FlushOnStop(t *testing.T) {
	repo := &fakeBatchRepo{}
	acc := NewUsageAccumulator(AccumulatorConfig{
		Repo:          repo,
		FlushInterval: time.Hour, // évite le flush périodique dans ce test
		MaxPending:    1_000_000,
	})
	acc.Start(context.Background())

	keyA := uuid.New()
	keyB := uuid.New()
	for i := 0; i < 100; i++ {
		acc.Incr(keyA)
	}
	for i := 0; i < 50; i++ {
		acc.Incr(keyB)
	}

	// Stop → flush final synchrone.
	acc.Stop(context.Background())

	assert.Equal(t, 150, repo.totalIncrs())
	stats := acc.Stats()
	assert.Equal(t, uint64(150), stats.FlushedIncrs)
}

func TestAccumulator_FlushOnTicker(t *testing.T) {
	repo := &fakeBatchRepo{}
	acc := NewUsageAccumulator(AccumulatorConfig{
		Repo:          repo,
		FlushInterval: 30 * time.Millisecond,
		MaxPending:    1_000_000,
	})
	acc.Start(context.Background())
	defer acc.Stop(context.Background())

	keyA := uuid.New()
	for i := 0; i < 10; i++ {
		acc.Incr(keyA)
	}

	// Attends au moins un tick.
	time.Sleep(100 * time.Millisecond)

	assert.GreaterOrEqual(t, repo.totalIncrs(), 10, "le ticker doit avoir flushé")
}

func TestAccumulator_FlushOnBackpressure(t *testing.T) {
	repo := &fakeBatchRepo{}
	acc := NewUsageAccumulator(AccumulatorConfig{
		Repo:          repo,
		FlushInterval: time.Hour, // pas de tick
		MaxPending:    5,          // seuil bas
	})
	acc.Start(context.Background())
	defer acc.Stop(context.Background())

	keyA := uuid.New()
	for i := 0; i < 20; i++ {
		acc.Incr(keyA)
	}

	// Les flushes anticipés sont asynchrones — attend un peu.
	require.Eventually(t, func() bool {
		return repo.flushes.Load() > 0
	}, time.Second, 10*time.Millisecond, "backpressure doit déclencher un flush")
}

func TestAccumulator_Stop_Idempotent(t *testing.T) {
	acc := NewUsageAccumulator(AccumulatorConfig{
		Repo:          &fakeBatchRepo{},
		FlushInterval: time.Hour,
	})
	acc.Start(context.Background())
	acc.Stop(context.Background())
	acc.Stop(context.Background()) // 2e appel : no-op, pas de panic
}

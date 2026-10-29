// usage_accumulator.go — batch des increments de usage_count pour éviter
// le hot row DB (fix P4).
//
// Au lieu d'UPDATE api_keys SET usage_count = usage_count + 1 WHERE id = X
// à chaque requête API key (lock DB à chaque fois), on accumule en mémoire
// et on flush par batch via BatchIncrementAPIKeyUsage.
//
// Un ticker flush toutes les `flushInterval`. Un flush supplémentaire est
// déclenché quand le compteur global atteint `maxPending` (backpressure).
// Au shutdown, `Stop()` flush une dernière fois de manière synchrone.
package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
)

// UsageAccumulator accumule les increments et les flush par batch.
type UsageAccumulator struct {
	repo          repository.AuthRepository
	logger        *zap.SugaredLogger
	flushInterval time.Duration
	maxPending    int64 // déclenche un flush anticipé si dépassé

	mu      sync.Mutex
	pending map[uuid.UUID]int

	pendingCount atomic.Int64 // total des increments en attente (pour backpressure)
	flushed      atomic.Uint64
	flushes      atomic.Uint64
	flushErrors  atomic.Uint64

	stopCh  chan struct{}
	stopped atomic.Bool
	wg      sync.WaitGroup
}

// AccumulatorConfig : paramètres du batcher.
type AccumulatorConfig struct {
	Repo          repository.AuthRepository
	Logger        *zap.SugaredLogger
	FlushInterval time.Duration // défaut 5s
	MaxPending    int64         // défaut 500 — backpressure
}

// NewUsageAccumulator construit l'accumulator. Start() doit être appelé pour
// démarrer le ticker de flush.
func NewUsageAccumulator(cfg AccumulatorConfig) *UsageAccumulator {
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = 500
	}
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &UsageAccumulator{
		repo:          cfg.Repo,
		logger:        logger.With("component", "apikey-usage-accumulator"),
		flushInterval: cfg.FlushInterval,
		maxPending:    cfg.MaxPending,
		pending:       make(map[uuid.UUID]int),
		stopCh:        make(chan struct{}),
	}
}

// Incr incrémente le compteur pour keyID. Thread-safe.
// Déclenche un flush anticipé si le total des increments en attente dépasse
// maxPending (backpressure).
func (a *UsageAccumulator) Incr(keyID uuid.UUID) {
	if a == nil || a.stopped.Load() {
		return
	}
	a.mu.Lock()
	a.pending[keyID]++
	a.mu.Unlock()

	if a.pendingCount.Add(1) >= a.maxPending {
		// Trigger un flush en arrière-plan (non-bloquant).
		go a.flush(context.Background())
	}
}

// Start lance la goroutine de flush périodique.
// Safe à appeler plusieurs fois (idempotent).
func (a *UsageAccumulator) Start(ctx context.Context) {
	if a == nil {
		return
	}
	a.wg.Add(1)
	go a.run(ctx)
}

// Stop arrête le ticker ET fait un flush final synchrone.
// À appeler au shutdown de l'App pour ne pas perdre les compteurs en attente.
func (a *UsageAccumulator) Stop(ctx context.Context) {
	if a == nil {
		return
	}
	if !a.stopped.CompareAndSwap(false, true) {
		return
	}
	close(a.stopCh)
	a.wg.Wait()
	// Flush final synchrone — capture tout ce qui est arrivé entre le dernier
	// tick et le Stop.
	a.flush(ctx)
	a.logger.Infow("usage accumulator stopped",
		"flushed", a.flushed.Load(),
		"flushes", a.flushes.Load(),
		"errors", a.flushErrors.Load(),
	)
}

// Stats snapshot.
type AccumulatorStats struct {
	FlushedIncrs  uint64
	FlushCount    uint64
	FlushErrors   uint64
	PendingCount  int64
	PendingUnique int
}

func (a *UsageAccumulator) Stats() AccumulatorStats {
	a.mu.Lock()
	unique := len(a.pending)
	a.mu.Unlock()
	return AccumulatorStats{
		FlushedIncrs:  a.flushed.Load(),
		FlushCount:    a.flushes.Load(),
		FlushErrors:   a.flushErrors.Load(),
		PendingCount:  a.pendingCount.Load(),
		PendingUnique: unique,
	}
}

func (a *UsageAccumulator) run(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(a.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.flush(ctx)
		}
	}
}

// flush : snapshot + reset de la map pending, puis batch UPDATE.
// Safe à appeler concurremment : la section critique est courte (copy + swap).
func (a *UsageAccumulator) flush(ctx context.Context) {
	a.mu.Lock()
	if len(a.pending) == 0 {
		a.mu.Unlock()
		return
	}
	snapshot := a.pending
	a.pending = make(map[uuid.UUID]int, len(snapshot)) // reset
	a.mu.Unlock()

	var totalIncrs int64
	for _, v := range snapshot {
		totalIncrs += int64(v)
	}
	// Décrémente pendingCount du montant total flushé.
	a.pendingCount.Add(-totalIncrs)

	// Budget par flush (évite de bloquer longtemps si DB lente).
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := a.repo.BatchIncrementAPIKeyUsage(flushCtx, snapshot); err != nil {
		a.flushErrors.Add(1)
		a.logger.Warnw("batch usage increment failed — counters lost",
			"error", err, "keys", len(snapshot), "incrs", totalIncrs)
		return
	}
	a.flushes.Add(1)
	a.flushed.Add(uint64(totalIncrs))
}

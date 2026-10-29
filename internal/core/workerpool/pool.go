// Package workerpool fournit un pool de workers borné pour exécuter des
// tâches asynchrones sans fuite de goroutines.
//
// Usage typique (updates DB "fire-and-forget") :
//
//     pool := workerpool.New(workerpool.Config{
//         Workers:   16,
//         QueueSize: 1024,
//         TaskTimeout: 5 * time.Second,
//         Logger:    logger,
//     })
//
//     // Côté handler HTTP :
//     pool.Submit(func(ctx context.Context) {
//         _ = repo.UpdateAPIKeyUsage(ctx, keyID)
//     })
//
//     // Côté shutdown :
//     pool.Shutdown(10 * time.Second) // drain queue + wait workers
//
// Avantages vs `go func() { ... }` naïf :
//   - Bornage du parallélisme (pas d'explosion si la DB est lente)
//   - Backpressure : Submit retourne false si queue pleine → l'appelant peut logger/drop
//   - Ctx par tâche (timeout hérité, pas lié à la request HTTP)
//   - Graceful shutdown : drain + timeout configurable
package workerpool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Config : paramètres de création du pool.
type Config struct {
	Workers     int           // nombre de goroutines workers
	QueueSize   int           // taille du buffered channel (pending tasks)
	TaskTimeout time.Duration // ctx.Timeout appliqué à chaque tâche (0 = pas de timeout)
	Logger      *zap.SugaredLogger
}

// Task : fonction à exécuter. Reçoit un ctx borné par TaskTimeout.
type Task func(ctx context.Context)

// Pool : worker pool borné avec graceful shutdown.
type Pool struct {
	cfg      Config
	tasks    chan Task
	wg       sync.WaitGroup
	logger   *zap.SugaredLogger
	closed   atomic.Bool
	dropped  atomic.Uint64 // compteur des tâches rejetées (queue pleine)
	executed atomic.Uint64
	failed   atomic.Uint64
}

// New construit et démarre un pool.
// Défauts : Workers=8, QueueSize=256, TaskTimeout=10s si nuls.
func New(cfg Config) *Pool {
	if cfg.Workers <= 0 {
		cfg.Workers = 8
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 256
	}
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = 10 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}

	p := &Pool{
		cfg:    cfg,
		tasks:  make(chan Task, cfg.QueueSize),
		logger: logger.With("component", "workerpool"),
	}
	for i := 0; i < cfg.Workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

// Submit ajoute une tâche à la queue. Non-bloquant.
// Retourne false si le pool est fermé OU si la queue est pleine —
// dans les deux cas, la tâche est jetée et le compteur `dropped` incrémenté.
func (p *Pool) Submit(task Task) bool {
	if p.closed.Load() {
		p.dropped.Add(1)
		return false
	}
	select {
	case p.tasks <- task:
		return true
	default:
		p.dropped.Add(1)
		return false
	}
}

// SubmitBlocking ajoute une tâche à la queue, bloque si elle est pleine.
// À utiliser avec parcimonie : bloque le caller (potentiellement un handler HTTP).
// Retourne false si le pool est fermé.
func (p *Pool) SubmitBlocking(task Task) bool {
	if p.closed.Load() {
		p.dropped.Add(1)
		return false
	}
	p.tasks <- task
	return true
}

// Shutdown ferme la queue et attend que les workers finissent les tâches en cours.
// Si le timeout est dépassé, retourne false (tâches pouvant être perdues).
func (p *Pool) Shutdown(timeout time.Duration) bool {
	if !p.closed.CompareAndSwap(false, true) {
		return true // déjà shutdown
	}
	close(p.tasks)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Infow("workerpool stopped gracefully",
			"executed", p.executed.Load(),
			"dropped", p.dropped.Load(),
			"failed", p.failed.Load(),
		)
		return true
	case <-time.After(timeout):
		p.logger.Warnw("workerpool shutdown timeout — tasks may be lost",
			"timeout", timeout,
			"executed", p.executed.Load(),
			"dropped", p.dropped.Load(),
		)
		return false
	}
}

// Stats : snapshot des métriques internes (pour logs/Prometheus).
type Stats struct {
	Executed uint64
	Dropped  uint64
	Failed   uint64
	Queued   int
}

func (p *Pool) Stats() Stats {
	return Stats{
		Executed: p.executed.Load(),
		Dropped:  p.dropped.Load(),
		Failed:   p.failed.Load(),
		Queued:   len(p.tasks),
	}
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for task := range p.tasks {
		p.runOne(task)
	}
}

func (p *Pool) runOne(task Task) {
	defer func() {
		if r := recover(); r != nil {
			p.failed.Add(1)
			p.logger.Errorw("workerpool task panic", "panic", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.TaskTimeout)
	defer cancel()
	task(ctx)
	p.executed.Add(1)
}

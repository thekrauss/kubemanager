package workerpool

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPool_SubmitAndShutdown(t *testing.T) {
	p := New(Config{Workers: 4, QueueSize: 64, TaskTimeout: time.Second})

	var counter atomic.Uint64
	// SubmitBlocking : déterministe, on ne drop pas sur contention.
	for i := 0; i < 50; i++ {
		ok := p.SubmitBlocking(func(_ context.Context) {
			counter.Add(1)
		})
		require.True(t, ok)
	}

	require.True(t, p.Shutdown(5*time.Second))
	assert.Equal(t, uint64(50), counter.Load())

	stats := p.Stats()
	assert.Equal(t, uint64(50), stats.Executed)
	assert.Zero(t, stats.Dropped)
	assert.Zero(t, stats.Failed)
}

func TestPool_QueueFull_DropsTask(t *testing.T) {
	// 1 worker, queue de 1 → toute Submit au-delà de 2 en vol est droppée.
	p := New(Config{Workers: 1, QueueSize: 1, TaskTimeout: time.Second})

	blocker := make(chan struct{})
	started := make(chan struct{})
	// Bloque le worker.
	require.True(t, p.Submit(func(_ context.Context) {
		close(started)
		<-blocker
	}))
	<-started // garantit que le worker a consommé la tâche

	// Remplit la queue (1 slot).
	require.True(t, p.Submit(func(_ context.Context) {}))

	// Les suivantes doivent toutes être droppées (queue + worker occupés).
	const overflow = 10
	var dropped int
	for i := 0; i < overflow; i++ {
		if !p.Submit(func(_ context.Context) {}) {
			dropped++
		}
	}
	assert.Equal(t, overflow, dropped, "toutes les tâches au-delà de la capacité doivent être droppées")

	close(blocker)
	p.Shutdown(time.Second)
	assert.Equal(t, uint64(overflow), p.Stats().Dropped)
}

func TestPool_Panic_Isolated(t *testing.T) {
	// 1 worker pour être déterministe sur l'ordre d'exécution.
	p := New(Config{Workers: 1, QueueSize: 16, TaskTimeout: time.Second})

	var okRuns atomic.Uint64
	var wg sync.WaitGroup

	// Panic dans une tâche — ne doit pas tuer le worker.
	wg.Add(1)
	require.True(t, p.SubmitBlocking(func(_ context.Context) {
		defer wg.Done()
		panic("boom")
	}))

	// Les tâches suivantes doivent s'exécuter normalement.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		require.True(t, p.SubmitBlocking(func(_ context.Context) {
			defer wg.Done()
			okRuns.Add(1)
		}))
	}

	wg.Wait()
	p.Shutdown(time.Second)

	assert.Equal(t, uint64(5), okRuns.Load())
	assert.Equal(t, uint64(1), p.Stats().Failed)
}

func TestPool_Submit_AfterShutdown_Fails(t *testing.T) {
	p := New(Config{Workers: 1, QueueSize: 1, TaskTimeout: time.Second})
	p.Shutdown(time.Second)

	ok := p.Submit(func(_ context.Context) {})
	assert.False(t, ok)
}

func TestPool_TaskTimeout_Respected(t *testing.T) {
	p := New(Config{Workers: 1, QueueSize: 1, TaskTimeout: 50 * time.Millisecond})

	deadlineCh := make(chan time.Time, 1)
	p.Submit(func(ctx context.Context) {
		if d, ok := ctx.Deadline(); ok {
			deadlineCh <- d
		}
	})
	p.Shutdown(time.Second)

	select {
	case deadline := <-deadlineCh:
		assert.True(t, time.Until(deadline) <= 50*time.Millisecond,
			"le ctx de la tâche doit avoir un deadline <= TaskTimeout")
	default:
		t.Fatal("tâche n'a pas eu de deadline")
	}
}

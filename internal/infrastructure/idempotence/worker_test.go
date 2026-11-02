package idempotency

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAtomic : verrou en mémoire, comme le gestionnaire Redis.
type fakeAtomic struct {
	inProgress map[string]bool
	failed     int
}

func (f *fakeAtomic) Lookup(context.Context, string, string, any) (bool, error) { return false, nil }
func (f *fakeAtomic) Store(context.Context, string, string, any) error           { return nil }
func (f *fakeAtomic) Hash(any, ...string) (string, error)                        { return "h", nil }
func (f *fakeAtomic) Begin(_ context.Context, key, _ string, _ time.Duration, _ any) (BeginStatus, error) {
	if f.inProgress[key] {
		return BeginInProgress, nil
	}
	f.inProgress[key] = true
	return BeginStarted, nil
}
func (f *fakeAtomic) Complete(_ context.Context, key, _ string, _ any) error {
	delete(f.inProgress, key)
	return nil
}
func (f *fakeAtomic) Fail(_ context.Context, key, _ string) error {
	f.failed++
	delete(f.inProgress, key)
	return nil
}

// Un panic devient une erreur et libère le verrou : la tentative suivante
// s'exécute (avant : « déjà en cours » jusqu'à l'expiration du verrou).
func TestExecuteWorker_PanicReleasesLock(t *testing.T) {
	mgr := &fakeAtomic{inProgress: map[string]bool{}}
	ctx := context.Background()
	_, _, err := ExecuteWorker(ctx, mgr, "k", "h", WorkerExecutionOptions{}, func() (int, error) {
		panic("cannot parse ''")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic")
	assert.Equal(t, 1, mgr.failed)

	got, status, err := ExecuteWorker(ctx, mgr, "k", "h", WorkerExecutionOptions{}, func() (int, error) { return 42, nil })
	require.NoError(t, err)
	assert.Equal(t, WorkerExecuted, status)
	assert.Equal(t, 42, got)
}

// ctxAtomic : n'accepte Fail et Complete que sur un contexte encore valide,
// comme Redis.
type ctxAtomic struct{ fakeAtomic }

func (f *ctxAtomic) Fail(ctx context.Context, key, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.fakeAtomic.Fail(ctx, key, hash)
}

func (f *ctxAtomic) Complete(ctx context.Context, key, hash string, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.fakeAtomic.Complete(ctx, key, hash, v)
}

// Annulée pendant le travail, une activité libère quand même son verrou (avant :
// « en cours » jusqu'à l'expiration).
func TestExecuteWorker_FinalizesOnCanceledContext(t *testing.T) {
	for _, workErr := range []error{context.Canceled, nil} {
		mgr := &ctxAtomic{fakeAtomic{inProgress: map[string]bool{}}}
		ctx, cancel := context.WithCancel(context.Background())
		_, _, err := ExecuteWorker(ctx, mgr, "k", "h", WorkerExecutionOptions{}, func() (int, error) {
			cancel()
			return 1, workErr
		})
		assert.ErrorIs(t, err, workErr)
		assert.Empty(t, mgr.inProgress)
	}
}

// Une nouvelle tentative reprend le verrou d'un worker tombé, s'il s'agit bien
// d'une tentative suivante d'une activité qui le demande.
func TestRetakeLock(t *testing.T) {
	mgr := &fakeAtomic{inProgress: map[string]bool{"k": true}}
	retakeLock(context.Background(), mgr, "k", "h", ActivityOptions{})
	status, err := mgr.Begin(context.Background(), "k", "h", time.Minute, nil)
	require.NoError(t, err)
	assert.Equal(t, BeginStarted, status)
}

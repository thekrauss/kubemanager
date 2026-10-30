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

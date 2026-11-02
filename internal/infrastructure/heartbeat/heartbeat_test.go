package heartbeat

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
)

// Heartbeats réguliers pendant l'opération, arrêtés à sa fin.
func TestBeatWhile(t *testing.T) {
	var beats atomic.Int64
	err := beatWhile(context.Background(), 5*time.Millisecond, func() { beats.Add(1) }, func() error {
		time.Sleep(60 * time.Millisecond)
		return nil
	})
	require.NoError(t, err)
	after := beats.Load()
	assert.Greater(t, after, int64(2))
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, after, beats.Load(), "plus aucun heartbeat après la fin")
}

// Dans une vraie activité.
func TestWhile(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	var beats atomic.Int64
	env.SetOnActivityHeartbeatListener(func(*activity.Info, converter.EncodedValues) { beats.Add(1) })
	work := func(ctx context.Context) error {
		return While(ctx, time.Millisecond, func() error { return nil })
	}
	env.RegisterActivity(work)
	_, err := env.ExecuteActivity(work)
	require.NoError(t, err)
	assert.Positive(t, beats.Load())
}

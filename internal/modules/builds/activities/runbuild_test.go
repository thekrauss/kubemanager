package activities

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// L'activité se signale dès la préparation du cluster : un cluster distant lent
// dépassait le délai de heartbeat avant le premier signal.
func TestRunBuild_HeartbeatsDuringSetup(t *testing.T) {
	k8s := fake.NewClientset()
	k8s.PrependReactor("create", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("cluster injoignable")
	})
	a := &Activities{K8s: k8s, Job: JobConfig{Namespace: "builds"}, Logger: zap.NewNop().Sugar()}

	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	var beats atomic.Int64
	env.SetOnActivityHeartbeatListener(func(*activity.Info, converter.EncodedValues) { beats.Add(1) })
	work := func(ctx context.Context) (*BuildResult, error) {
		return a.RunBuild(ctx, BuildRef{BuildID: "b", ProjectID: "p"})
	}
	env.RegisterActivity(work)
	_, err := env.ExecuteActivity(work)
	require.Error(t, err)
	assert.Positive(t, beats.Load())
}

// LimitBytes garderait le début du journal, sans l'erreur finale.
func TestSavedLogOptions_ReadsWholeLog(t *testing.T) {
	opts := savedLogOptions()
	assert.Nil(t, opts.LimitBytes)
	assert.Nil(t, opts.TailLines)
	assert.Equal(t, containerName, opts.Container)
}

func TestLogTail(t *testing.T) {
	write := func(limit int, chunks ...string) string {
		tail := &logTail{limit: limit}
		for _, c := range chunks {
			n, err := tail.Write([]byte(c))
			require.NoError(t, err)
			require.Equal(t, len(c), n)
		}
		return string(tail.Bytes())
	}

	t.Run("sous la limite : intact", func(t *testing.T) {
		assert.Equal(t, "un\ndeux\n", write(32, "un\n", "deux\n"))
		assert.Equal(t, "", write(32))
	})
	t.Run("exactement la limite : intact", func(t *testing.T) {
		assert.Equal(t, "un\ndeux\n", write(8, "un\n", "deux\n"))
	})
	t.Run("au-dessus : la fin, à partir d'une ligne entière", func(t *testing.T) {
		assert.Equal(t, savedLogCutMarker+"erreur\n", write(10, "début\n", "milieu\n", "erreur\n"))
	})
	t.Run("un seul envoi plus grand que la limite", func(t *testing.T) {
		assert.Equal(t, savedLogCutMarker+"erreur\n", write(10, "début\nmilieu\nerreur\n"))
	})
	t.Run("aucun retour à la ligne", func(t *testing.T) {
		assert.Equal(t, savedLogCutMarker+"6789", write(4, "0123456789"))
	})
	t.Run("mémoire bornée", func(t *testing.T) {
		tail := &logTail{limit: 80}
		line := []byte("ligne de journal\n")
		for range 10000 {
			_, _ = tail.Write(line)
			assert.LessOrEqual(t, len(tail.buf), 90)
		}
		assert.True(t, strings.HasSuffix(string(tail.Bytes()), string(line)))
		assert.LessOrEqual(t, len(tail.Bytes()), 80+len(savedLogCutMarker))
	})
}

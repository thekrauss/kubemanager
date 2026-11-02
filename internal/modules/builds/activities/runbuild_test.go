package activities

import (
	"context"
	"errors"
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

func TestSavedLogOptions_Bounded(t *testing.T) {
	opts := savedLogOptions()
	require.NotNil(t, opts.LimitBytes)
	assert.EqualValues(t, maxSavedLogBytes, *opts.LimitBytes)
	assert.Equal(t, containerName, opts.Container)
}

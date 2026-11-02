package workflows

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/thekrauss/kubemanager/internal/modules/builds/activities"
	"github.com/thekrauss/kubemanager/internal/modules/builds/domain"
)

func runWorkflow(t *testing.T, runErr error, wantStatus, wantFailure, wantDigest string) {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	var acts *activities.Activities
	env.RegisterActivity(acts)
	env.OnActivity(acts.MarkBuilding, mock.Anything, mock.Anything).Return(nil)
	if runErr != nil {
		env.OnActivity(acts.RunBuild, mock.Anything, mock.Anything).Return(nil, runErr)
	} else {
		env.OnActivity(acts.RunBuild, mock.Anything, mock.Anything).Return(&activities.BuildResult{Digest: wantDigest}, nil)
	}
	var got activities.FinishInput
	env.OnActivity(acts.Finish, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.FinishInput) error {
		got = in
		return nil
	})
	env.ExecuteWorkflow(BuildImageWorkflow, BuildInput{BuildRef: activities.BuildRef{BuildID: "b", ProjectID: "p"}, Deadline: time.Minute})
	require.True(t, env.IsWorkflowCompleted())
	assert.Equal(t, wantStatus, got.Status)
	assert.Equal(t, wantFailure, got.Failure)
	assert.Equal(t, wantDigest, got.Digest)
}

func TestBuildWorkflow_Success(t *testing.T) {
	runWorkflow(t, nil, domain.StatusSucceeded, "", "sha256:abc")
}

// L'échec du Job se retrouve dans l'état final, et Finish tourne toujours.
func TestBuildWorkflow_Failures(t *testing.T) {
	for _, code := range []string{domain.FailureBuild, domain.FailureTimeout, domain.FailureDisk} {
		runWorkflow(t, temporal.NewNonRetryableApplicationError("failed", code, nil), domain.StatusFailed, code, "")
	}
	runWorkflow(t, temporal.NewNonRetryableApplicationError("boom", "OTHER", nil), domain.StatusFailed, domain.FailureInternal, "")
}

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

type deployRun struct {
	deployed, notDeployed int
	recorded              string
}

func runDeployWorkflow(t *testing.T, runErr, deployErr error) deployRun {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	var acts *activities.Activities
	var out deployRun
	env.RegisterActivity(acts)
	env.OnActivity(acts.MarkBuilding, mock.Anything, mock.Anything).Return(nil)
	if runErr != nil {
		env.OnActivity(acts.RunBuild, mock.Anything, mock.Anything).Return(nil, runErr)
	} else {
		env.OnActivity(acts.RunBuild, mock.Anything, mock.Anything).Return(&activities.BuildResult{}, nil)
	}
	env.OnActivity(acts.Finish, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.DeployBuilt, mock.Anything, mock.Anything).Return(func(context.Context, activities.BuildRef) error {
		out.deployed++
		return deployErr
	}).Maybe()
	env.OnActivity(acts.BuildNotDeployed, mock.Anything, mock.Anything).Return(func(context.Context, activities.NotDeployedInput) error {
		out.notDeployed++
		return nil
	}).Maybe()
	env.OnActivity(acts.RecordDeployFailure, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.DeployFailedInput) error {
		out.recorded = in.State
		return nil
	}).Maybe()
	env.ExecuteWorkflow(BuildImageWorkflow, BuildInput{BuildRef: activities.BuildRef{BuildID: "b", ProjectID: "p"}, Deadline: time.Minute, Deploy: true})
	require.True(t, env.IsWorkflowCompleted())
	return out
}

// Build réussi : le serveur déploie, sans passer par le navigateur.
func TestBuildWorkflow_DeploysOnSuccess(t *testing.T) {
	got := runDeployWorkflow(t, nil, nil)
	assert.Equal(t, 1, got.deployed)
	assert.Zero(t, got.notDeployed)
	assert.Empty(t, got.recorded)
}

// Build en échec : rien n'est déployé, l'actuel reste en service.
func TestBuildWorkflow_NoDeployOnFailure(t *testing.T) {
	got := runDeployWorkflow(t, temporal.NewNonRetryableApplicationError("failed", domain.FailureBuild, nil), nil)
	assert.Zero(t, got.deployed)
	assert.Equal(t, 1, got.notDeployed)
}

// Conteneur occupé : réessais bornés, puis état clair.
func TestBuildWorkflow_BusyIsRetriedThenRecorded(t *testing.T) {
	got := runDeployWorkflow(t, nil, temporal.NewApplicationError("busy", domain.DeployBusy))
	assert.Equal(t, deployMaxAttempts, got.deployed)
	assert.Equal(t, domain.DeployBusy, got.recorded)
}

// Refus définitif (quota, image) : pas de réessai.
func TestBuildWorkflow_RefusalNotRetried(t *testing.T) {
	got := runDeployWorkflow(t, nil, temporal.NewNonRetryableApplicationError("refused", domain.DeployFailed, nil))
	assert.Equal(t, 1, got.deployed)
	assert.Equal(t, domain.DeployFailed, got.recorded)
}

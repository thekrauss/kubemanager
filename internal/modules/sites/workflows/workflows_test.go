package workflows

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/thekrauss/kubemanager/internal/modules/sites/activities"
)

// fakeActs : mêmes signatures exactes que SiteActivities (ctx context.Context).
type fakeActs struct {
	extractErr  error
	readyCalls  int
	failedMsg   string
	archiveGone bool
}

func (f *fakeActs) ExtractDeployment(_ context.Context, _ activities.DeploymentRef) (activities.ExtractResult, error) {
	return activities.ExtractResult{FileCount: 2, SizeBytes: 10}, f.extractErr
}
func (f *fakeActs) MarkDeploymentReady(_ context.Context, _ activities.ReadyInput) error {
	f.readyCalls++
	return nil
}
func (f *fakeActs) MarkDeploymentFailed(_ context.Context, in activities.FailedInput) error {
	f.failedMsg = in.Message
	return nil
}
func (f *fakeActs) DeleteArchive(_ context.Context, _ activities.DeploymentRef) error {
	f.archiveGone = true
	return nil
}

func runDeploy(t *testing.T, f *fakeActs) error {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(f.ExtractDeployment, activityName("ExtractDeployment"))
	env.RegisterActivityWithOptions(f.MarkDeploymentReady, activityName("MarkDeploymentReady"))
	env.RegisterActivityWithOptions(f.MarkDeploymentFailed, activityName("MarkDeploymentFailed"))
	env.RegisterActivityWithOptions(f.DeleteArchive, activityName("DeleteArchive"))
	env.ExecuteWorkflow(DeploySiteWorkflow, activities.DeploymentRef{SiteID: "s", DeploymentID: "d"})
	require.True(t, env.IsWorkflowCompleted())
	return env.GetWorkflowError()
}

func TestDeploySiteWorkflow_HappyPath(t *testing.T) {
	f := &fakeActs{}
	require.NoError(t, runDeploy(t, f))
	assert.Equal(t, 1, f.readyCalls)
	assert.True(t, f.archiveGone)
	assert.Empty(t, f.failedMsg)
}

func TestDeploySiteWorkflow_InvalidArchiveShowsUserMessage(t *testing.T) {
	f := &fakeActs{extractErr: temporal.NewNonRetryableApplicationError(
		"index.html introuvable à la racine de l'archive", "InvalidArchive", nil)}
	assert.Error(t, runDeploy(t, f))
	assert.Zero(t, f.readyCalls, "rien n'est activé")
	assert.True(t, f.archiveGone)
	assert.Equal(t, "index.html introuvable à la racine de l'archive", f.failedMsg)
}

func TestDeploySiteWorkflow_InfraErrorStaysInternal(t *testing.T) {
	f := &fakeActs{extractErr: temporal.NewNonRetryableApplicationError("dial tcp 10.0.0.3:9000: refused", "Infra", nil)}
	assert.Error(t, runDeploy(t, f))
	assert.Equal(t, genericDeployError, f.failedMsg)
}

func activityName(name string) activity.RegisterOptions {
	return activity.RegisterOptions{Name: name}
}

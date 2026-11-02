package workflows

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// Fakes dédupliqués des activités réelles (ProjectK8sActivities +
// ProjectDBActivities) : Temporal fait la résolution par nom de méthode.

type fakeDeleteActs struct {
	mu sync.Mutex

	deleteNSErr        error
	deleteWorkloadsN   int64
	deleteWorkloadsErr error
	deleteDBErr        error
	deleteSitesErr     error

	deleteNSCalls        int
	deleteWorkloadsCalls int
	deleteDBCalls        int
	deleteWorkloadsAfter bool
	deleteSitesCalls     int
	dbDeletedBeforeSites bool
}

func (f *fakeDeleteActs) DeleteProjectSiteObjects(_ context.Context, _ string) error {
	f.mu.Lock()
	f.deleteSitesCalls++
	f.dbDeletedBeforeSites = f.deleteDBCalls > 0
	f.mu.Unlock()
	return f.deleteSitesErr
}

func (f *fakeDeleteActs) DeleteNamespace(_ context.Context, _ string) error {
	f.mu.Lock()
	f.deleteNSCalls++
	f.mu.Unlock()
	return f.deleteNSErr
}

func (f *fakeDeleteActs) DeleteProjectWorkloadsActivity(_ context.Context, _ string) (int64, error) {
	f.mu.Lock()
	f.deleteWorkloadsCalls++
	// Check that namespace was deleted BEFORE — orderedness assertion.
	f.deleteWorkloadsAfter = f.deleteNSCalls >= 1
	f.mu.Unlock()
	if f.deleteWorkloadsErr != nil {
		return 0, f.deleteWorkloadsErr
	}
	return f.deleteWorkloadsN, nil
}

func (f *fakeDeleteActs) DeleteProjectDBActivity(_ context.Context, _ string) error {
	f.mu.Lock()
	f.deleteDBCalls++
	f.mu.Unlock()
	return f.deleteDBErr
}

func registerFakeDeleteActs(env *testsuite.TestWorkflowEnvironment, f *fakeDeleteActs) {
	env.RegisterActivity(f.DeleteNamespace)
	env.RegisterActivity(f.DeleteProjectWorkloadsActivity)
	env.RegisterActivity(f.DeleteProjectDBActivity)
	env.RegisterActivity(f.DeleteProjectSiteObjects)
}

func TestDeleteProjectWorkflow_HappyPath_OrderedCleanup(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	fake := &fakeDeleteActs{deleteWorkloadsN: 2}
	registerFakeDeleteActs(env, fake)

	env.ExecuteWorkflow(DeleteProjectWorkflow, "proj-uuid", "alpha")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, 1, fake.deleteNSCalls)
	assert.Equal(t, 1, fake.deleteWorkloadsCalls)
	assert.Equal(t, 1, fake.deleteDBCalls)
	assert.True(t, fake.deleteWorkloadsAfter, "DB cleanup must run AFTER namespace delete")
	assert.Equal(t, 1, fake.deleteSitesCalls)
	assert.False(t, fake.dbDeletedBeforeSites, "site files must be deleted BEFORE the project row")
}

func TestDeleteProjectWorkflow_SiteCleanupFails_KeepsProjectRow(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetTestTimeout(5 * time.Second)

	fake := &fakeDeleteActs{deleteSitesErr: errors.New("s3 down")}
	registerFakeDeleteActs(env, fake)

	env.ExecuteWorkflow(DeleteProjectWorkflow, "proj-uuid", "alpha")

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Equal(t, 0, fake.deleteDBCalls, "project row must survive so the cleanup can be retried")
}

func TestDeleteProjectWorkflow_NamespaceFails_AbortsBeforeDB(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetTestTimeout(5 * time.Second)

	fake := &fakeDeleteActs{deleteNSErr: errors.New("K8s unreachable")}
	registerFakeDeleteActs(env, fake)

	env.ExecuteWorkflow(DeleteProjectWorkflow, "proj-uuid", "alpha")

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Equal(t, 0, fake.deleteWorkloadsCalls, "DB cleanup must not run if namespace delete fails")
	assert.Equal(t, 0, fake.deleteDBCalls)
}

func TestDeleteProjectWorkflow_WorkloadCleanupFails_AbortsBeforeProjectDelete(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetTestTimeout(5 * time.Second)

	fake := &fakeDeleteActs{deleteWorkloadsErr: errors.New("db down")}
	registerFakeDeleteActs(env, fake)

	env.ExecuteWorkflow(DeleteProjectWorkflow, "proj-uuid", "alpha")

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Equal(t, 1, fake.deleteNSCalls)
	assert.Equal(t, 0, fake.deleteDBCalls, "project row must survive if workload cleanup fails (can retry)")
}

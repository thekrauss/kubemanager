package workflows

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
)

// fakeProjectActs fournit des méthodes identiques (noms + signatures) à
// ProjectActivities, pour que Temporal les matche via reflection lors de
// l'appel ExecuteActivity/ExecuteLocalActivity dans le workflow.
type fakeProjectActs struct {
	mu sync.Mutex

	createInDBID        uuid.UUID
	createInDBErr       error
	createdNamespace    bool
	createNSErr         error
	reconcileErr        error
	assignOwnerErr      error
	dbDeleteCalled      bool
	reconcileNS         string
	reconcileCPU        string
	reconcileMem        string
	assignProjectID    string
	assignUserID       string
}

func (f *fakeProjectActs) CreateProjectInDB(_ context.Context, name, description, ownerID string) (*dauth.Project, error) {
	if f.createInDBErr != nil {
		return nil, f.createInDBErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &dauth.Project{ID: f.createInDBID, Name: name, Description: description}, nil
}

func (f *fakeProjectActs) CreateNamespace(_ context.Context, projectID, projectName string) error {
	f.mu.Lock()
	f.createdNamespace = true
	f.mu.Unlock()
	return f.createNSErr
}

func (f *fakeProjectActs) DeleteProjectDBActivity(_ context.Context, projectID string) error {
	f.mu.Lock()
	f.dbDeleteCalled = true
	f.mu.Unlock()
	return nil
}

func (f *fakeProjectActs) ReconcileProjectResources(_ context.Context, ns, cpu, mem string) error {
	f.mu.Lock()
	f.reconcileNS = ns
	f.reconcileCPU = cpu
	f.reconcileMem = mem
	f.mu.Unlock()
	return f.reconcileErr
}

func (f *fakeProjectActs) AssignDefaultOwner(_ context.Context, projectID, userID string) error {
	f.mu.Lock()
	f.assignProjectID = projectID
	f.assignUserID = userID
	f.mu.Unlock()
	return f.assignOwnerErr
}

// ── tests ─────────────────────────────────────────────────────────────────

func registerFakeProjectActs(env *testsuite.TestWorkflowEnvironment, f *fakeProjectActs) {
	env.RegisterActivity(f.CreateProjectInDB)
	env.RegisterActivity(f.CreateNamespace)
	env.RegisterActivity(f.DeleteProjectDBActivity)
	env.RegisterActivity(f.ReconcileProjectResources)
	env.RegisterActivity(f.AssignDefaultOwner)
}

func TestCreateProjectWorkflow_HappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	projectUUID := uuid.New()
	fake := &fakeProjectActs{createInDBID: projectUUID}
	registerFakeProjectActs(env, fake)

	env.ExecuteWorkflow(CreateProjectWorkflow, CreateProjectInput{
		Name:        "alpha",
		Description: "demo",
		OwnerID:     "owner-42",
		CpuLimit:    "1000m",
		MemoryLimit: "2Gi",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result ProjectResult
	require.NoError(t, env.GetWorkflowResult(&result))

	assert.Equal(t, projectUUID.String(), result.ProjectID)
	assert.Equal(t, "km-alpha", result.Namespace)
	assert.Equal(t, utils.ProjectStatusReady, result.Status)

	// Compensation must NOT have run on the happy path.
	assert.False(t, fake.dbDeleteCalled)

	// Reconcile got the right namespace + limits.
	assert.Equal(t, "km-alpha", fake.reconcileNS)
	assert.Equal(t, "1000m", fake.reconcileCPU)
	assert.Equal(t, "2Gi", fake.reconcileMem)

	// Owner assignment wired with proper IDs.
	assert.Equal(t, projectUUID.String(), fake.assignProjectID)
	assert.Equal(t, "owner-42", fake.assignUserID)
}

func TestCreateProjectWorkflow_NamespaceFailure_CompensatesDBRow(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetTestTimeout(5 * time.Second)

	projectUUID := uuid.New()
	fake := &fakeProjectActs{
		createInDBID: projectUUID,
		createNSErr:  errors.New("no capacity"),
	}
	registerFakeProjectActs(env, fake)

	env.ExecuteWorkflow(CreateProjectWorkflow, CreateProjectInput{
		Name:        "alpha",
		OwnerID:     "owner-42",
		CpuLimit:    "1000m",
		MemoryLimit: "2Gi",
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rolled back")

	// Compensation: DB row deleted after namespace failure.
	assert.True(t, fake.dbDeleteCalled)
}

func TestCreateProjectWorkflow_CreateInDBFailure_Aborts(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetTestTimeout(5 * time.Second)

	fake := &fakeProjectActs{createInDBErr: errors.New("db down")}
	registerFakeProjectActs(env, fake)

	env.ExecuteWorkflow(CreateProjectWorkflow, CreateProjectInput{Name: "alpha", OwnerID: "o"})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())

	// Aucun side-effect K8s — le workflow a abort avant.
	assert.False(t, fake.createdNamespace)
	assert.False(t, fake.dbDeleteCalled)
}

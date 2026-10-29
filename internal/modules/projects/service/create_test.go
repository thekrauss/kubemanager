package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	typesproject "github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
)

// ── fakes ─────────────────────────────────────────────────────────────────

type fakeDistributor struct {
	gotWorkflow any
	gotArgs     []any
	gotOpts     temporal.ExecuteOptions
	handle      temporal.WorkflowHandle
	err         error
}

func (f *fakeDistributor) ExecuteWorkflow(_ context.Context, opts temporal.ExecuteOptions, wf any, args ...any) (temporal.WorkflowHandle, error) {
	f.gotOpts = opts
	f.gotWorkflow = wf
	f.gotArgs = args
	if f.err != nil {
		return temporal.WorkflowHandle{}, f.err
	}
	return f.handle, nil
}

func (f *fakeDistributor) SignalWorkflow(_ context.Context, _ string, _ string, _ string, _ any) error {
	return nil
}

func (f *fakeDistributor) QueryWorkflow(_ context.Context, _ string, _ string, _ string, _ ...any) (converter.EncodedValue, error) {
	return nil, nil
}

func (f *fakeDistributor) CancelWorkflow(_ context.Context, _ string, _ string) error {
	return nil
}

func (f *fakeDistributor) TerminateWorkflow(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

type fakeProjectRepo struct {
	project *authdomain.Project
	getErr  error
	created []*authdomain.Project
	deleted []string
}

func (f *fakeProjectRepo) CreateProject(_ context.Context, p *authdomain.Project, _ string) error {
	f.created = append(f.created, p)
	return nil
}
func (f *fakeProjectRepo) DeleteProject(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeProjectRepo) GetProjectByID(_ context.Context, _ string) (*authdomain.Project, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.project, nil
}

// ── tests ─────────────────────────────────────────────────────────────────

func newProjectServiceForTest(dist temporal.TaskDistributor, repo *fakeProjectRepo) *ProjectService {
	return &ProjectService{
		Distributor: dist,
		Config:      &configs.GlobalConfig{},
		Logger:      zap.NewNop().Sugar(),
		Repos:       repo,
	}
}

func TestCreateProject_StartsWorkflowAndReturnsProvisioning(t *testing.T) {
	dist := &fakeDistributor{handle: temporal.WorkflowHandle{WorkflowID: "wf-123", RunID: "run-1"}}
	repo := &fakeProjectRepo{}
	svc := newProjectServiceForTest(dist, repo)

	req := typesproject.CreateProjectRequest{
		Name:        "alpha",
		Description: "demo",
		CpuLimit:    "1000m",
		MemoryLimit: "2Gi",
	}
	resp, err := svc.CreateProject(context.Background(), req, "owner-42")

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "wf-123", resp.WorkflowID)
	assert.Equal(t, utils.ProjectStatusProvisioning, resp.Status)
	assert.Contains(t, dist.gotOpts.WorkflowID, "project-create-")
	require.Len(t, dist.gotArgs, 1)
}

func TestCreateProject_WorkflowError_PropagatesAndNoResponse(t *testing.T) {
	dist := &fakeDistributor{err: errors.New("temporal down")}
	svc := newProjectServiceForTest(dist, &fakeProjectRepo{})

	resp, err := svc.CreateProject(context.Background(), typesproject.CreateProjectRequest{Name: "x"}, "o")

	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestDeleteProject_NotFound_ReturnsError(t *testing.T) {
	dist := &fakeDistributor{}
	repo := &fakeProjectRepo{getErr: errors.New("not found")}
	svc := newProjectServiceForTest(dist, repo)

	resp, err := svc.DeleteProject(context.Background(), uuid.NewString())

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Nil(t, dist.gotWorkflow, "workflow must not start when project missing")
}

func TestDeleteProject_HappyPath_StartsDeleteWorkflow(t *testing.T) {
	projectID := uuid.NewString()
	dist := &fakeDistributor{handle: temporal.WorkflowHandle{WorkflowID: "wf-del"}}
	repo := &fakeProjectRepo{project: &authdomain.Project{
		ID:   uuid.MustParse(projectID),
		Name: "alpha",
	}}
	svc := newProjectServiceForTest(dist, repo)

	resp, err := svc.DeleteProject(context.Background(), projectID)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "DELETING", resp.Status)
	assert.Equal(t, projectID, resp.ProjectID)
	assert.Equal(t, "project-delete-"+projectID, dist.gotOpts.WorkflowID)
	assert.Equal(t, "DeleteProjectWorkflow", dist.gotWorkflow)
	require.Len(t, dist.gotArgs, 2)
	assert.Equal(t, projectID, dist.gotArgs[0])
	assert.Equal(t, "alpha", dist.gotArgs[1])
}

package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/thekrauss/kubemanager/internal/modules/projects/workflows"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/converter"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	typesproject "github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	workloadRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
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

func (f *fakeDistributor) IsRunning(context.Context, string) (bool, error) {
	return false, nil
}

func (f *fakeDistributor) CancelWorkflow(_ context.Context, _ string, _ string) error {
	return nil
}

func (f *fakeDistributor) TerminateWorkflow(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (f *fakeProjectRepo) UpdateProjectStatus(context.Context, string, string) error { return nil }

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

// L'identifiant du projet est connu tout de suite (le bootstrap en a besoin)
// et c'est celui que le workflow écrira en base ; limites par défaut.
func TestCreateProject_ReturnsIDAndDefaults(t *testing.T) {
	dist := &fakeDistributor{handle: temporal.WorkflowHandle{WorkflowID: "wf"}}
	svc := newProjectServiceForTest(dist, &fakeProjectRepo{})

	resp, err := svc.CreateProject(context.Background(), typesproject.CreateProjectRequest{Name: "alpha"}, "owner-42")
	require.NoError(t, err)
	_, err = uuid.Parse(resp.ProjectID)
	require.NoError(t, err, "identifiant renvoyé immédiatement")
	input := dist.gotArgs[0].(workflows.CreateProjectInput)
	assert.Equal(t, resp.ProjectID, input.ProjectID)
	assert.Equal(t, "project-create-"+resp.ProjectID, dist.gotOpts.WorkflowID)
	assert.Equal(t, DefaultProjectCPU, input.CpuLimit)
	assert.Equal(t, DefaultProjectMemory, input.MemoryLimit)

	_, err = svc.CreateProject(context.Background(), typesproject.CreateProjectRequest{Name: "beta", CpuLimit: "beaucoup"}, "owner-42")
	assert.ErrorIs(t, err, ErrInvalidProjectLimits)
}

// Deuxième demande de suppression : déjà en cours, même réponse (l'option
// d'erreur du distributeur ne doit pas la transformer en échec).
func TestDeleteProject_AlreadyRunning(t *testing.T) {
	projectID := uuid.NewString()
	dist := &fakeDistributor{err: fmt.Errorf("execute workflow: %w", serviceerror.NewWorkflowExecutionAlreadyStarted("started", "", "run"))}
	repo := &fakeProjectRepo{project: &authdomain.Project{ID: uuid.MustParse(projectID), Name: "alpha"}}
	resp, err := newProjectServiceForTest(dist, repo).DeleteProject(context.Background(), projectID)
	require.NoError(t, err)
	assert.Equal(t, workflows.WorkflowIDDeletePrefix+projectID, resp.WorkflowID)
}

type failingUsage struct {
	workloadRepo.WorkloadRepository
	err error
}

func (f failingUsage) GetTotalUsageByProject(context.Context, uuid.UUID) (int64, int64, int64, error) {
	return 0, 0, 0, f.err
}

// Une panne ne s'affiche pas comme une réservation nulle.
func TestGetMetrics_UsageError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"PodMetricsList","apiVersion":"metrics.k8s.io/v1beta1","items":[]}`))
	}))
	defer srv.Close()
	down := errors.New("db down")
	svc := newProjectServiceForTest(&fakeDistributor{}, &fakeProjectRepo{project: &authdomain.Project{ID: uuid.New(), Name: "alpha"}})
	svc.K8sConfig = &rest.Config{Host: srv.URL}
	svc.WorkloadRepo = failingUsage{err: down}
	_, err := svc.GetMetrics(context.Background(), uuid.NewString())
	require.ErrorIs(t, err, down)
}

package activities

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
)

// ── fakes ─────────────────────────────────────────────────────────────────

type fakeRepo struct {
	mu        sync.Mutex
	updated   []struct{ Status, Phase string }
	deleted   []uuid.UUID
	updateErr error
	deleteErr error
}

func (f *fakeRepo) Create(_ context.Context, _ *domain.Workload) error { return nil }
func (f *fakeRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Workload, error) {
	return nil, nil
}
func (f *fakeRepo) ListByProject(_ context.Context, _ uuid.UUID) ([]domain.Workload, error) {
	return nil, nil
}
func (f *fakeRepo) UpdateStatus(_ context.Context, _ uuid.UUID, status, phase string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.mu.Lock()
	f.updated = append(f.updated, struct{ Status, Phase string }{status, phase})
	f.mu.Unlock()
	return nil
}
func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	f.deleted = append(f.deleted, id)
	f.mu.Unlock()
	return nil
}
func (f *fakeRepo) UpdateSpec(_ context.Context, _ uuid.UUID, _, _, _ string, _ []byte) error {
	return nil
}
func (f *fakeRepo) UpdateRuntime(_ context.Context, _ uuid.UUID, _ int, _, _ string, _ int, _ bool) error {
	return nil
}
func (f *fakeRepo) GetTotalUsageByProject(_ context.Context, _ uuid.UUID) (int64, int64, int64, error) {
	return 0, 0, 0, nil
}
func (f *fakeRepo) DeleteByProject(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

type capturePublisher struct {
	mu     sync.Mutex
	events []pubCall
	err    error
	failOn string // channel pattern substring that should fail
}

type pubCall struct {
	Channel string
	Data    any
}

func (p *capturePublisher) Publish(_ context.Context, channel string, data any) error {
	p.mu.Lock()
	p.events = append(p.events, pubCall{Channel: channel, Data: data})
	p.mu.Unlock()
	if p.failOn != "" && strings.Contains(channel, p.failOn) {
		return errors.New("boom")
	}
	return p.err
}

// ── tests ─────────────────────────────────────────────────────────────────

func newActs(repo *fakeRepo, pub *capturePublisher) *WorkloadDBActivities {
	return &WorkloadDBActivities{
		Logger:    zap.NewNop().Sugar(),
		Repo:      repo,
		Publisher: pub,
	}
}

func TestUpdateWorkloadStatus_HappyPath(t *testing.T) {
	wID := uuid.New().String()
	pID := uuid.New().String()
	repo := &fakeRepo{}
	pub := &capturePublisher{}
	acts := newActs(repo, pub)

	err := acts.UpdateWorkloadStatus(context.Background(), wID, pID, "RUNNING", "DEPLOYED")
	require.NoError(t, err)

	require.Len(t, repo.updated, 1)
	assert.Equal(t, "RUNNING", repo.updated[0].Status)
	assert.Equal(t, "DEPLOYED", repo.updated[0].Phase)

	require.Len(t, pub.events, 2, "expected workload + project channel publishes")
	assert.Contains(t, pub.events[0].Channel, "workload:"+wID+":deploy")
	assert.Contains(t, pub.events[1].Channel, "project:"+pID+":events")

	evt, ok := pub.events[0].Data.(DeploymentEvent)
	require.True(t, ok)
	assert.Equal(t, "RUNNING", evt.Status)
	assert.Equal(t, "DEPLOYED", evt.Phase)
	assert.Equal(t, wID, evt.WorkloadID)
	assert.Equal(t, pID, evt.ProjectID)
	assert.False(t, evt.At.IsZero())
}

func TestUpdateWorkloadStatus_InvalidID_Errors(t *testing.T) {
	repo := &fakeRepo{}
	acts := newActs(repo, &capturePublisher{})

	err := acts.UpdateWorkloadStatus(context.Background(), "not-a-uuid", "p", "X", "Y")

	require.Error(t, err)
	assert.Empty(t, repo.updated)
}

func TestUpdateWorkloadStatus_DBFailure_NoBroadcast(t *testing.T) {
	repo := &fakeRepo{updateErr: errors.New("db down")}
	pub := &capturePublisher{}
	acts := newActs(repo, pub)

	err := acts.UpdateWorkloadStatus(context.Background(), uuid.New().String(), uuid.New().String(), "X", "Y")

	require.Error(t, err)
	assert.Empty(t, pub.events, "broadcast must not happen when DB fails")
}

func TestUpdateWorkloadStatus_PublisherError_DoesNotFailActivity(t *testing.T) {
	repo := &fakeRepo{}
	pub := &capturePublisher{err: errors.New("centrifugo down")}
	acts := newActs(repo, pub)

	err := acts.UpdateWorkloadStatus(context.Background(), uuid.New().String(), uuid.New().String(), "RUNNING", "OK")

	// Publisher errors must be silently swallowed (fail-open).
	require.NoError(t, err)
	assert.Len(t, repo.updated, 1)
	assert.Len(t, pub.events, 2, "activity still attempts both publishes")
}

func TestUpdateWorkloadStatus_NilPublisher_NoCrash(t *testing.T) {
	repo := &fakeRepo{}
	acts := &WorkloadDBActivities{Logger: zap.NewNop().Sugar(), Repo: repo, Publisher: nil}

	err := acts.UpdateWorkloadStatus(context.Background(), uuid.New().String(), uuid.New().String(), "RUNNING", "OK")

	require.NoError(t, err)
	assert.Len(t, repo.updated, 1)
}

func TestUpdateWorkloadStatus_EmptyProjectID_SkipsBroadcast(t *testing.T) {
	repo := &fakeRepo{}
	pub := &capturePublisher{}
	acts := newActs(repo, pub)

	err := acts.UpdateWorkloadStatus(context.Background(), uuid.New().String(), "", "RUNNING", "OK")

	require.NoError(t, err)
	assert.Empty(t, pub.events, "no projectID → no channel, skip broadcast")
}

// ── SoftDeleteWorkload ────────────────────────────────────────────────────

func TestSoftDeleteWorkload_HappyPath(t *testing.T) {
	wID := uuid.New()
	pID := uuid.New().String()
	repo := &fakeRepo{}
	pub := &capturePublisher{}
	acts := newActs(repo, pub)

	err := acts.SoftDeleteWorkload(context.Background(), wID.String(), pID)

	require.NoError(t, err)
	require.Len(t, repo.deleted, 1)
	assert.Equal(t, wID, repo.deleted[0])

	require.Len(t, pub.events, 2)
	evt := pub.events[0].Data.(DeploymentEvent)
	assert.Equal(t, "DELETED", evt.Status)
	assert.Equal(t, "REMOVED", evt.Phase)
}

func TestSoftDeleteWorkload_DBFailure_NoBroadcast(t *testing.T) {
	repo := &fakeRepo{deleteErr: errors.New("db down")}
	pub := &capturePublisher{}
	acts := newActs(repo, pub)

	err := acts.SoftDeleteWorkload(context.Background(), uuid.New().String(), uuid.New().String())

	require.Error(t, err)
	assert.Empty(t, pub.events)
}

// Transition refusée (conteneur en suppression) : erreur définitive, le workflow
// s'arrête au lieu de relancer l'activité.
func TestUpdateWorkloadStatus_DeletingIsNotRetried(t *testing.T) {
	acts := newActs(&fakeRepo{updateErr: domain.ErrWorkloadDeleting}, nil)
	err := acts.UpdateWorkloadStatus(context.Background(), uuid.NewString(), uuid.NewString(), "RUNNING", "DEPLOYED")
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.True(t, appErr.NonRetryable())
}

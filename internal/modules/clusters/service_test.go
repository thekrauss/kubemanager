package clusters

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
)

// ── Mocks minimaux ────────────────────────────────────────────────────────

// fakeWorkloadRepo : embed l'interface pour fail-loud sur les méthodes non overridées.
type fakeWorkloadRepo struct {
	repository.WorkloadRepository
	byID map[uuid.UUID]*domain.Workload
	mu   sync.Mutex
	// Enregistrement des UpdateStatus pour assertion.
	statusUpdates []statusUpdate
}
type statusUpdate struct {
	ID     uuid.UUID
	Status string
	Phase  string
}

func (f *fakeWorkloadRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Workload, error) {
	if w, ok := f.byID[id]; ok {
		return w, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeWorkloadRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status, phase string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusUpdates = append(f.statusUpdates, statusUpdate{id, status, phase})
	return nil
}

// capturePublisher enregistre tous les Publish pour inspection.
type capturePublisher struct {
	mu        sync.Mutex
	published []publishedEvent
}
type publishedEvent struct {
	Channel string
	Data    any
}

func (p *capturePublisher) Publish(ctx context.Context, channel string, data any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, publishedEvent{channel, data})
	return nil
}

// fakeClusterRepo : in-memory ClusterRepository pour les tests.
type fakeClusterRepo struct {
	mu       sync.Mutex
	byID     map[string]*Cluster
	touched  []string
}

func newFakeClusterRepo() *fakeClusterRepo {
	return &fakeClusterRepo{byID: make(map[string]*Cluster)}
}

func (f *fakeClusterRepo) Upsert(ctx context.Context, clusterID, version string) (*Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	if c, ok := f.byID[clusterID]; ok {
		c.Version = version
		c.LastSeenAt = &now
		c.UpdatedAt = now
		return c, nil
	}
	c := &Cluster{
		ID:         uuid.New(),
		ClusterID:  clusterID,
		Version:    version,
		LastSeenAt: &now,
		EnrolledAt: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	f.byID[clusterID] = c
	return c, nil
}
func (f *fakeClusterRepo) GetByClusterID(ctx context.Context, clusterID string) (*Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.byID[clusterID]; ok {
		return c, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeClusterRepo) List(ctx context.Context) ([]Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Cluster, 0, len(f.byID))
	for _, c := range f.byID {
		out = append(out, *c)
	}
	return out, nil
}
func (f *fakeClusterRepo) TouchLastSeen(ctx context.Context, clusterID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, clusterID)
	return nil
}

// ── Tests ─────────────────────────────────────────────────────────────────

func TestExtractWorkloadID(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"km-workload-id primary", map[string]string{"km-workload-id": "abc"}, "abc"},
		{"workload-id fallback", map[string]string{"workload-id": "fallback"}, "fallback"},
		{"km- takes priority", map[string]string{"km-workload-id": "p", "workload-id": "q"}, "p"},
		{"empty", nil, ""},
		{"whitespace trimmed", map[string]string{"km-workload-id": "  spaced  "}, "spaced"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, extractWorkloadID(PodEvent{Labels: c.labels}))
		})
	}
}

func TestReasonToStatus_Mapping(t *testing.T) {
	cases := []struct {
		reason string
		want   string
	}{
		{"CrashLoopBackOff", "CRASHLOOP"},
		{"OOMKilled", "OOM_KILLED"},
		{"ImagePullBackOff", "IMAGE_PULL_FAILED"},
		{"Evicted", "EVICTED"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, reasonToWorkloadStatus[c.reason], "reason=%s", c.reason)
	}
}

func TestOnPodEvent_UnknownWorkload_Ignored(t *testing.T) {
	repo := &fakeWorkloadRepo{byID: map[uuid.UUID]*domain.Workload{}}
	pub := &capturePublisher{}
	svc := NewService(repo, newFakeClusterRepo(), pub, zap.NewNop().Sugar())

	ack, err := svc.OnPodEvent(context.Background(), "cluster-1", PodEvent{
		EventID: "e1",
		Name:    "pod-abc",
		Reason:  "CrashLoopBackOff",
		// Pas de labels → pas de workloadID
	})
	require.NoError(t, err)
	assert.Equal(t, "ignored", ack.Status)
	assert.Empty(t, repo.statusUpdates)
	assert.Empty(t, pub.published)
}

func TestOnPodEvent_MappedReason_UpdatesAndBroadcasts(t *testing.T) {
	workloadID := uuid.New()
	projectID := uuid.New()
	repo := &fakeWorkloadRepo{
		byID: map[uuid.UUID]*domain.Workload{
			workloadID: {
				ID:        workloadID,
				ProjectID: projectID,
				Name:      "myapp",
				Namespace: "km-demo",
			},
		},
	}
	pub := &capturePublisher{}
	svc := NewService(repo, newFakeClusterRepo(), pub, zap.NewNop().Sugar())

	ack, err := svc.OnPodEvent(context.Background(), "cluster-1", PodEvent{
		EventID: "e2",
		Name:    "myapp-abc",
		Reason:  "CrashLoopBackOff",
		Phase:   "Failed",
		RestartCount: 5,
		Labels: map[string]string{
			"km-workload-id": workloadID.String(),
			"km-project-id":  projectID.String(),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "mapped", ack.Status)
	assert.Equal(t, workloadID.String(), ack.WorkloadID)

	// UpdateStatus appelé avec le bon mapping
	require.Len(t, repo.statusUpdates, 1)
	assert.Equal(t, workloadID, repo.statusUpdates[0].ID)
	assert.Equal(t, "CRASHLOOP", repo.statusUpdates[0].Status)
	assert.Equal(t, "POD_CrashLoopBackOff", repo.statusUpdates[0].Phase)

	// Publish Centrifugo appelé (1× sur le channel deploy).
	require.Len(t, pub.published, 1)
	assert.Contains(t, pub.published[0].Channel, "workload:"+workloadID.String()+":deploy")
}

func TestOnPodEvent_UnmappedReason_AcceptedNoUpdate(t *testing.T) {
	workloadID := uuid.New()
	repo := &fakeWorkloadRepo{
		byID: map[uuid.UUID]*domain.Workload{
			workloadID: {ID: workloadID, ProjectID: uuid.New(), Name: "app", Namespace: "km-x"},
		},
	}
	pub := &capturePublisher{}
	svc := NewService(repo, newFakeClusterRepo(), pub, zap.NewNop().Sugar())

	ack, err := svc.OnPodEvent(context.Background(), "c1", PodEvent{
		EventID: "e3",
		Reason:  "SomeObscureReason",
		Labels:  map[string]string{"km-workload-id": workloadID.String()},
	})
	require.NoError(t, err)
	assert.Equal(t, "accepted", ack.Status)
	// Pas d'UpdateStatus car reason non mappée.
	assert.Empty(t, repo.statusUpdates)
	// Mais un broadcast quand même (l'UI doit voir l'event brut).
	require.Len(t, pub.published, 1)
}

func TestEnroll_FirstTimeAndUpdate(t *testing.T) {
	repo := &fakeWorkloadRepo{}
	pub := &capturePublisher{}
	svc := NewService(repo, newFakeClusterRepo(), pub, zap.NewNop().Sugar())

	r1, err := svc.Enroll(context.Background(), EnrollRequest{ClusterID: "c1", Version: "v1.0.0"})
	require.NoError(t, err)
	assert.Equal(t, "enrolled", r1.Status)

	r2, err := svc.Enroll(context.Background(), EnrollRequest{ClusterID: "c1", Version: "v1.0.1"})
	require.NoError(t, err)
	assert.Equal(t, "updated", r2.Status)
}

func TestGetConfig_Defaults(t *testing.T) {
	svc := NewService(&fakeWorkloadRepo{}, newFakeClusterRepo(), &capturePublisher{}, zap.NewNop().Sugar())
	cfg, err := svc.GetConfig(context.Background(), "c1")
	require.NoError(t, err)
	assert.Equal(t, 60, cfg.DedupCooldownSeconds)
	assert.Equal(t, 50, cfg.BatchSize)
	assert.Equal(t, 1000, cfg.BatchFlushMs)
	assert.Contains(t, cfg.EnabledReasons, "CrashLoopBackOff")
	assert.Contains(t, cfg.EnabledReasons, "OOMKilled")
}

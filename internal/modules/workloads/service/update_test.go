package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
)

type fakeDistributor struct {
	temporal.TaskDistributor
	args []any
}

func (f *fakeDistributor) ExecuteWorkflow(_ context.Context, _ temporal.ExecuteOptions, _ any, args ...any) (temporal.WorkflowHandle, error) {
	f.args = args
	return temporal.WorkflowHandle{}, nil
}

type fakeRepo struct {
	repository.WorkloadRepository
	w                            domain.Workload
	specImage, specDisk, specURL string
}

func (f *fakeRepo) GetByID(context.Context, uuid.UUID) (*domain.Workload, error) {
	w := f.w
	return &w, nil
}
func (f *fakeRepo) UpdateSpec(_ context.Context, _ uuid.UUID, image, disk, url string) error {
	f.specImage, f.specDisk, f.specURL = image, disk, url
	return nil
}

// Nouvelle version : seule l'image change, le reste de la configuration
// enregistrée est repris (avant : port, ressources, réplicas, TLS et URL
// perdus, image jamais enregistrée).
func TestUpdate_KeepsSavedConfiguration(t *testing.T) {
	saved := configs.AppConfig
	defer func() { configs.AppConfig = saved }()
	configs.AppConfig.Vps.AdressIp = "5.196.223.196"

	project := uuid.MustParse("37f62891-bdce-40b1-be27-833c1fea931c")
	repo := &fakeRepo{w: domain.Workload{
		ID: uuid.New(), ProjectID: project, Name: "web", Namespace: "km-sunupay",
		Image: "nginx:1.26", Replicas: 2, TargetPort: 3000, TLSEnabled: true,
		CPULimit: "500m", MemoryLimit: "512Mi", CPURequest: "100m", MemoryRequest: "128Mi",
		StorageSize: "1Gi", StorageClass: "local-path",
	}}
	dist := &fakeDistributor{}
	s := &WorkloadService{Repo: repo, Distributor: dist}

	require.NoError(t, s.Update(context.Background(), repo.w.ID.String(), domain.UpdateWorkloadRequest{Image: "nginx:1.27"}))

	assert.Equal(t, "nginx:1.27", repo.specImage, "nouvelle image enregistrée")
	assert.Equal(t, "1Gi", repo.specDisk)
	assert.Equal(t, "https://web-37f628.5.196.223.196.sslip.io", repo.specURL, "URL enregistrée")
	require.Len(t, dist.args, 1)
	in := dist.args[0].(workflows.DeployWorkloadInput)
	assert.Equal(t, "nginx:1.27", in.Image)
	assert.Equal(t, "web", in.ReleaseName)
	assert.Equal(t, 2, in.Replicas)
	assert.Equal(t, 3000, in.TargetPort)
	assert.Equal(t, "500m", in.CPULimit)
	assert.Equal(t, "512Mi", in.MemoryLimit)
	assert.True(t, in.TLSEnabled)
	assert.Equal(t, "web-37f628.5.196.223.196.sslip.io", in.ExternalHost, "URL publique conservée")

	err := s.Update(context.Background(), repo.w.ID.String(), domain.UpdateWorkloadRequest{StorageSize: "512Mi"})
	assert.Error(t, err, "disque réduit refusé")
	assert.Error(t, s.Update(context.Background(), "pas-un-uuid", domain.UpdateWorkloadRequest{}))
}

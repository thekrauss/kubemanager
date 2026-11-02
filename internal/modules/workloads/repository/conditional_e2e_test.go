package repository

// Écritures conditionnelles sur un vrai Postgres (conditional_test.go n'en vérifie
// que la forme, avec un faux pilote). Ignoré sans IAM_E2E :
//
//	bash scripts/iam_e2e_db.sh
//	IAM_E2E=1 go test ./internal/modules/workloads/repository/ -run TestE2E -count=1 -v

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
)

const e2eDSN = "postgres://postgres:pw@127.0.0.1:55433/postgres?sslmode=disable"

// e2eProject : dépôt sur la vraie base et projet propre au test (rejouable).
func e2eProject(t *testing.T) (WorkloadRepository, *gorm.DB, uuid.UUID) {
	t.Helper()
	if os.Getenv("IAM_E2E") == "" {
		t.Skip("IAM_E2E not set")
	}
	db, err := gorm.Open(postgres.Open(e2eDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	project := uuid.New()
	// Organisation du jeu de données de scripts/iam_e2e_db.sh.
	require.NoError(t, db.Exec(`INSERT INTO projects (id, name, organization_id)
		SELECT ?, ?, organization_id FROM projects LIMIT 1`, project, "e2e-"+project.String()).Error)
	return NewWorkloadRepository(db), db, project
}

func e2eWorkload(project uuid.UUID, name string) *domain.Workload {
	return &domain.Workload{
		ProjectID: project, Name: name, Namespace: "km-e2e", ChartName: "standard-app", Version: "1",
		Image: "nginx:1", Status: utils.WorkloadStarting, CurrentPhase: utils.PhaseHelmChartLoading, Replicas: 1,
	}
}

func e2eCreate(t *testing.T, repo WorkloadRepository, project uuid.UUID, name string) *domain.Workload {
	t.Helper()
	w := e2eWorkload(project, name)
	require.NoError(t, repo.Create(context.Background(), w))
	return w
}

// En suppression : toute écriture est refusée, et la ligne ne bouge vraiment pas.
func TestE2E_WritesRefusedWhileDeleting(t *testing.T) {
	repo, _, project := e2eProject(t)
	ctx := context.Background()
	for _, status := range []string{utils.WorkloadDeleting, utils.WorkloadDeleted} {
		w := e2eCreate(t, repo, project, "web-"+status)
		require.NoError(t, repo.UpdateStatus(ctx, w.ID, status, utils.PhaseHelmUninstalling))

		assert.ErrorIs(t, repo.UpdateStatus(ctx, w.ID, utils.WorkloadRunning, utils.PhaseDeployed), domain.ErrWorkloadDeleting)
		assert.ErrorIs(t, repo.UpdateRuntime(ctx, w.ID, 9, "900m", "900Mi", 9000, true), domain.ErrWorkloadDeleting)
		assert.ErrorIs(t, repo.UpdateSpec(ctx, w.ID, "nginx:2", "9Gi", "https://x", []byte(`{}`)), domain.ErrWorkloadDeleting)

		got, err := repo.GetByID(ctx, w.ID)
		require.NoError(t, err)
		assert.Equal(t, status, got.Status)
		assert.Equal(t, utils.PhaseHelmUninstalling, got.CurrentPhase)
		assert.Equal(t, "nginx:1", got.Image)
		assert.Equal(t, 1, got.Replicas)
	}
}

// Conteneur vivant : les écritures passent et touchent les bonnes colonnes.
func TestE2E_WritesAppliedWhenLive(t *testing.T) {
	repo, _, project := e2eProject(t)
	ctx := context.Background()
	w := e2eCreate(t, repo, project, "web")

	require.NoError(t, repo.UpdateStatus(ctx, w.ID, utils.WorkloadRunning, utils.PhaseDeployed))
	require.NoError(t, repo.UpdateRuntime(ctx, w.ID, 2, "300m", "128Mi", 8081, true))
	require.NoError(t, repo.UpdateSpec(ctx, w.ID, "nginx:2", "2Gi", "https://web.example", []byte(`{"image":"nginx:2"}`)))

	got, err := repo.GetByID(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, utils.WorkloadRunning, got.Status, "UpdateSpec laisse le statut du workflow")
	assert.Equal(t, utils.PhaseDeployed, got.CurrentPhase)
	assert.Equal(t, "nginx:2", got.Image)
	assert.Equal(t, 2, got.Replicas)
	assert.Equal(t, "300m", got.CPULimit)
	assert.JSONEq(t, `{"image":"nginx:2"}`, string(got.Spec))
}

// Une ligne absente n'est jamais une réussite silencieuse.
func TestE2E_MissingWorkloadIsRefused(t *testing.T) {
	repo, _, _ := e2eProject(t)
	err := repo.UpdateStatus(context.Background(), uuid.New(), utils.WorkloadRunning, utils.PhaseDeployed)
	assert.ErrorIs(t, err, domain.ErrWorkloadDeleting)
}

// Reprise d'une suppression en échec : DELETE_FAILED puis DELETING.
func TestE2E_DeleteStatusesResume(t *testing.T) {
	repo, _, project := e2eProject(t)
	ctx := context.Background()
	w := e2eCreate(t, repo, project, "web")
	for _, status := range []string{utils.WorkloadDeleting, utils.WorkloadDeleteFailed, utils.WorkloadDeleting} {
		require.NoError(t, repo.UpdateStatus(ctx, w.ID, status, utils.PhaseHelmUninstalling))
		got, err := repo.GetByID(ctx, w.ID)
		require.NoError(t, err)
		assert.Equal(t, status, got.Status)
	}
}

// La fin d'un déploiement croise la suppression : quel que soit l'ordre réel des
// écritures, le conteneur finit DELETING, jamais RUNNING.
func TestE2E_DeletingWinsTheRace(t *testing.T) {
	repo, _, project := e2eProject(t)
	ctx := context.Background()
	for range 50 {
		w := e2eCreate(t, repo, project, "web-"+uuid.NewString()[:8])
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			err := repo.UpdateStatus(ctx, w.ID, utils.WorkloadRunning, utils.PhaseDeployed)
			if err != nil && !errors.Is(err, domain.ErrWorkloadDeleting) {
				t.Errorf("écriture du déploiement : %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := repo.UpdateStatus(ctx, w.ID, utils.WorkloadDeleting, utils.PhaseHelmUninstalling); err != nil {
				t.Errorf("écriture de la suppression : %v", err)
			}
		}()
		wg.Wait()
		got, err := repo.GetByID(ctx, w.ID)
		require.NoError(t, err)
		require.Equal(t, utils.WorkloadDeleting, got.Status)
	}
}

// Deux créations simultanées du même nom : une seule passe (release Helm partagée).
func TestE2E_SameNameCreatedOnce(t *testing.T) {
	repo, _, project := e2eProject(t)
	const attempts = 8
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- repo.Create(context.Background(), e2eWorkload(project, "web"))
		}()
	}
	wg.Wait()
	close(results)
	created := 0
	for err := range results {
		if err == nil {
			created++
			continue
		}
		assert.ErrorIs(t, err, ErrNameTaken)
	}
	assert.Equal(t, 1, created)

	listed, err := repo.ListByProject(context.Background(), project)
	require.NoError(t, err)
	assert.Len(t, listed, 1)
}

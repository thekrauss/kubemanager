package workflows

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
)

// deployRun : transitions de statut réellement exécutées par le workflow.
type deployRun struct {
	mu       sync.Mutex
	statuses [][2]string
	install  int
}

func (r *deployRun) record(_ context.Context, _, _, status, phase string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, [2]string{status, phase})
	return nil
}

func (r *deployRun) last() [2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statuses[len(r.statuses)-1]
}

type deployMocks struct {
	syncErr, pullErr, installErr error
	// installWait : l'installation attend son annulation.
	installWait bool
	// statusErr : la transition de cet état échoue.
	statusErr map[string]error
	onEnv     func(env *testsuite.TestWorkflowEnvironment)
}

func runDeploy(t *testing.T, m deployMocks, in DeployWorkloadInput) (*deployRun, error) {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	var db *activities.WorkloadDBActivities
	var helm *activities.WorkloadActivities
	env.RegisterActivity(db)
	env.RegisterActivity(helm)
	run := &deployRun{}
	env.OnActivity(db.UpdateWorkloadStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, id, project, status, phase string) error {
			if err := m.statusErr[status]; err != nil {
				return err
			}
			return run.record(ctx, id, project, status, phase)
		})
	env.OnActivity(helm.SyncEnvSecret, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(m.syncErr)
	env.OnActivity(helm.EnsureRegistryPullSecret, mock.Anything, mock.Anything, mock.Anything).Return(m.pullErr)
	env.OnActivity(helm.InstallChart, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, _ activities.InstallWorkloadInput) error {
			run.mu.Lock()
			run.install++
			run.mu.Unlock()
			if m.installWait {
				<-ctx.Done()
				return ctx.Err()
			}
			return m.installErr
		})
	if m.onEnv != nil {
		m.onEnv(env)
	}
	env.ExecuteWorkflow(DeployWorkloadWorkflow, in)
	require.True(t, env.IsWorkflowCompleted())
	return run, env.GetWorkflowError()
}

func deployInput() DeployWorkloadInput {
	return DeployWorkloadInput{WorkloadID: "w", ProjectID: "p", Namespace: "km-x", ReleaseName: "web", Image: "nginx:1.27", TargetPort: 8080, Replicas: 1}
}

func nonRetryable(msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, "TEST", nil)
}

func TestDeploy_Success(t *testing.T) {
	run, err := runDeploy(t, deployMocks{}, deployInput())
	require.NoError(t, err)
	assert.Equal(t, 1, run.install)
	assert.Equal(t, [2]string{utils.WorkloadRunning, utils.PhaseDeployed}, run.last())
}

// Chaque échec laisse FAILED en base : sans attendre l'activité, le workflow se
// fermait, la transition n'avait jamais lieu et le conteneur restait STARTING.
func TestDeploy_FailureIsRecorded(t *testing.T) {
	platform := deployInput()
	platform.PlatformImage = true
	cases := []struct {
		name  string
		mocks deployMocks
		in    DeployWorkloadInput
		phase string
	}{
		{"secret des variables", deployMocks{syncErr: nonRetryable("sync")}, deployInput(), utils.PhaseSecretCreateError},
		{"secret de pull", deployMocks{pullErr: nonRetryable("pull")}, platform, utils.PhaseSecretCreateError},
		{"helm", deployMocks{installErr: nonRetryable("helm")}, deployInput(), utils.PhaseHelmError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run, err := runDeploy(t, c.mocks, c.in)
			require.Error(t, err)
			assert.Equal(t, [2]string{utils.WorkloadFailed, c.phase}, run.last())
		})
	}
}

// Erreur d'infrastructure : nouvelles tentatives. Application qui ne démarre pas
// (erreur définitive de l'activité) : une seule attente.
func TestDeploy_InstallAttempts(t *testing.T) {
	run, err := runDeploy(t, deployMocks{installErr: errors.New("cluster injoignable")}, deployInput())
	require.Error(t, err)
	assert.Equal(t, activityRetryMaximumAttempts, run.install)
	assert.Equal(t, [2]string{utils.WorkloadFailed, utils.PhaseHelmError}, run.last())

	run, err = runDeploy(t, deployMocks{installErr: nonRetryable("ne démarre pas")}, deployInput())
	require.Error(t, err)
	assert.Equal(t, 1, run.install)
	assert.Equal(t, [2]string{utils.WorkloadFailed, utils.PhaseHelmError}, run.last())
}

// Image illisible : refus avant toute écriture dans le cluster, phase dédiée.
func TestDeploy_InvalidImage(t *testing.T) {
	in := deployInput()
	in.Image = "   "
	run, err := runDeploy(t, deployMocks{}, in)
	require.Error(t, err)
	assert.Zero(t, run.install)
	assert.Equal(t, [2]string{utils.WorkloadFailed, utils.PhaseImageParseError}, run.last())
}

// RUNNING non enregistré : le workflow échoue au lieu de se dire réussi.
func TestDeploy_RunningWriteFailure(t *testing.T) {
	run, err := runDeploy(t, deployMocks{statusErr: map[string]error{utils.WorkloadRunning: nonRetryable("db")}}, deployInput())
	require.Error(t, err)
	assert.Equal(t, [2]string{utils.WorkloadFailed, utils.PhaseHelmError}, run.last())
}

// Première transition refusée (conteneur en suppression) : rien n'est installé.
func TestDeploy_StopsWhenFirstWriteRefused(t *testing.T) {
	run, err := runDeploy(t, deployMocks{statusErr: map[string]error{utils.WorkloadStarting: nonRetryable("deleting")}}, deployInput())
	require.Error(t, err)
	assert.Zero(t, run.install)
}

// Annulé pendant Helm : l'installation est interrompue et l'état final écrit
// malgré l'annulation (contexte détaché).
func TestDeploy_CancelInterruptsInstall(t *testing.T) {
	var canceled bool
	run, err := runDeploy(t, deployMocks{installWait: true, onEnv: func(env *testsuite.TestWorkflowEnvironment) {
		env.RegisterDelayedCallback(func() {
			canceled = true
			env.CancelWorkflow()
		}, 2*time.Second)
	}}, deployInput())
	require.True(t, canceled)
	require.Error(t, err)
	var canceledErr *temporal.CanceledError
	assert.True(t, errors.As(err, &canceledErr), "workflow annulé : %v", err)
	assert.Equal(t, [2]string{utils.WorkloadFailed, utils.PhaseHelmError}, run.last())
}

// Le délai de Helm reste nettement sous celui de l'activité : Temporal ne
// relance jamais pendant que Helm tourne encore.
func TestInstallTimeoutsAreOrdered(t *testing.T) {
	assert.GreaterOrEqual(t, installActivityTimeout, activities.HelmInstallTimeout+time.Minute)
	assert.Greater(t, activities.InstallChartProcessingTTL, installActivityTimeout)
}

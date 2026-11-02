package activities

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	helmtime "helm.sh/helm/v3/pkg/time"

	"github.com/thekrauss/kubemanager/internal/infrastructure/helm/charts"
)

func fakeHelm() *action.Configuration {
	return &action.Configuration{
		Releases:     storage.Init(driver.NewMemory()),
		KubeClient:   &kubefake.PrintingKubeClient{Out: io.Discard},
		Capabilities: chartutil.DefaultCapabilities,
		Log:          func(string, ...interface{}) {},
	}
}

// Premier déploiement = installation, puis mises à jour (avant : le premier
// déploiement échouait toujours, Upgrade.Install étant ignoré par le SDK).
func TestUpgradeOrInstall(t *testing.T) {
	ch, err := charts.StandardApp()
	require.NoError(t, err)
	cfg := fakeHelm()
	vals := buildHelmValues(InstallWorkloadInput{ReleaseName: "web", ImageRepo: "nginx", ImageTag: "1", TargetPort: 8080, Replicas: 1})

	require.NoError(t, upgradeOrInstall(context.Background(), cfg, "km-test", "web", ch, vals, false))
	rel, err := cfg.Releases.Last("web")
	require.NoError(t, err)
	assert.Equal(t, 1, rel.Version)
	assert.Equal(t, release.StatusDeployed, rel.Info.Status)

	require.NoError(t, upgradeOrInstall(context.Background(), cfg, "km-test", "web", ch, vals, false))
	rel, _ = cfg.Releases.Last("web")
	assert.Equal(t, 2, rel.Version, "deuxième déploiement : mise à jour")

	// Seulement une tentative en échec : réinstallée par-dessus.
	failedCfg := fakeHelm()
	require.NoError(t, failedCfg.Releases.Create(&release.Release{Name: "api", Namespace: "km-test", Version: 1,
		Info: &release.Info{Status: release.StatusFailed}, Chart: ch}))
	require.NoError(t, upgradeOrInstall(context.Background(), failedCfg, "km-test", "api", ch, vals, false))
	rel, _ = failedCfg.Releases.Last("api")
	assert.Equal(t, release.StatusDeployed, rel.Info.Status)
}

// Une annulation interrompt l'attente de Helm : c'est ce que la suppression d'un
// conteneur attend (avant : Run sans contexte, jusqu'au bout du délai).
func TestUpgradeOrInstall_CancelInterruptsWait(t *testing.T) {
	ch, err := charts.StandardApp()
	require.NoError(t, err)
	vals := buildHelmValues(InstallWorkloadInput{ReleaseName: "web", ImageRepo: "nginx", ImageTag: "1", TargetPort: 8080, Replicas: 1})
	slow := func(cfg *action.Configuration) {
		cfg.KubeClient = &kubefake.FailingKubeClient{PrintingKubeClient: kubefake.PrintingKubeClient{Out: io.Discard}, WaitDuration: 5 * time.Second}
	}

	run := func(t *testing.T, cfg *action.Configuration) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := upgradeOrInstall(ctx, cfg, "km-test", "web", ch, vals, false)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Less(t, time.Since(start), 3*time.Second)
	}

	t.Run("installation", func(t *testing.T) {
		cfg := fakeHelm()
		slow(cfg)
		run(t, cfg)
	})
	t.Run("mise à jour", func(t *testing.T) {
		cfg := fakeHelm()
		require.NoError(t, upgradeOrInstall(context.Background(), cfg, "km-test", "web", ch, vals, false))
		slow(cfg)
		run(t, cfg)
	})
}

// Release restée pending-* : débloquée à la nouvelle tentative ou une fois trop
// ancienne pour qu'une opération tourne encore, jamais avant.
func TestUpgradeOrInstall_PendingRelease(t *testing.T) {
	ch, err := charts.StandardApp()
	require.NoError(t, err)
	vals := buildHelmValues(InstallWorkloadInput{ReleaseName: "web", ImageRepo: "nginx", ImageTag: "1", TargetPort: 8080, Replicas: 1})
	old := helmtime.Time{Time: time.Now().Add(-helmPendingStaleAfter - time.Minute)}
	cases := []struct {
		name    string
		status  release.Status
		at      helmtime.Time
		retried bool
		wantOK  bool
	}{
		{"première tentative, opération récente", release.StatusPendingUpgrade, helmtime.Now(), false, false},
		{"nouvelle tentative", release.StatusPendingUpgrade, helmtime.Now(), true, true},
		{"installation interrompue, nouvelle tentative", release.StatusPendingInstall, helmtime.Now(), true, true},
		{"état trop ancien", release.StatusPendingUpgrade, old, false, true},
		{"retour arrière récent", release.StatusPendingRollback, helmtime.Now(), true, false},
		{"retour arrière trop ancien", release.StatusPendingRollback, old, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := fakeHelm()
			require.NoError(t, cfg.Releases.Create(&release.Release{Name: "web", Namespace: "km-test", Version: 1,
				Info: &release.Info{Status: c.status, LastDeployed: c.at}, Chart: ch, Config: map[string]any{}}))
			err := upgradeOrInstall(context.Background(), cfg, "km-test", "web", ch, vals, c.retried)
			if !c.wantOK {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "another operation")
				return
			}
			require.NoError(t, err)
			rel, err := cfg.Releases.Last("web")
			require.NoError(t, err)
			assert.Equal(t, release.StatusDeployed, rel.Info.Status)
		})
	}
}

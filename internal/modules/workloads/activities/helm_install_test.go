package activities

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"

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

	require.NoError(t, upgradeOrInstall(cfg, "km-test", "web", ch, vals))
	rel, err := cfg.Releases.Last("web")
	require.NoError(t, err)
	assert.Equal(t, 1, rel.Version)
	assert.Equal(t, release.StatusDeployed, rel.Info.Status)

	require.NoError(t, upgradeOrInstall(cfg, "km-test", "web", ch, vals))
	rel, _ = cfg.Releases.Last("web")
	assert.Equal(t, 2, rel.Version, "deuxième déploiement : mise à jour")

	// Seulement une tentative en échec : réinstallée par-dessus.
	failedCfg := fakeHelm()
	require.NoError(t, failedCfg.Releases.Create(&release.Release{Name: "api", Namespace: "km-test", Version: 1,
		Info: &release.Info{Status: release.StatusFailed}, Chart: ch}))
	require.NoError(t, upgradeOrInstall(failedCfg, "km-test", "api", ch, vals))
	rel, _ = failedCfg.Releases.Last("api")
	assert.Equal(t, release.StatusDeployed, rel.Info.Status)
}

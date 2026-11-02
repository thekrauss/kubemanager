package router

import (
	"errors"

	"github.com/thekrauss/kubemanager/internal/modules/builds"
	buildActs "github.com/thekrauss/kubemanager/internal/modules/builds/activities"
	"github.com/thekrauss/kubemanager/internal/modules/builds/registry"
	buildsRepo "github.com/thekrauss/kubemanager/internal/modules/builds/repository"
	buildsSvc "github.com/thekrauss/kubemanager/internal/modules/builds/service"
	workloadActs "github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
)

// minBuildSecretBytes : longueur minimale du secret du registre.
const minBuildSecretBytes = 32

// buildsEnabled : le module a besoin du stockage objet (archives, journaux).
func (a *App) buildsEnabled() bool { return a.Config.Builds.Enabled && a.ObjectStore != nil }

// newBuildController : nil si les builds sont désactivés ; une configuration
// incomplète empêche le démarrage plutôt que d'exposer un registre mal protégé.
func (a *App) newBuildController() (*builds.Controller, error) {
	if !a.buildsEnabled() {
		return nil, nil
	}
	c := a.Config.Builds.WithDefaults()
	if len(c.Secret) < minBuildSecretBytes {
		return nil, errors.New("builds.secret must be at least 32 bytes")
	}
	issuer, err := registry.NewIssuer(c.TokenIssuer, c.RegistryService, c.TokenCertFile, c.TokenKeyFile)
	if err != nil {
		return nil, err
	}
	return &builds.Controller{
		Service: &buildsSvc.Service{
			Repo:        buildsRepo.New(a.DB),
			Store:       a.ObjectStore,
			Distributor: a.TemporalDistributor,
			K8s:         a.K8sProvider.Client,
			Creds:       registry.Credentials{Secret: []byte(c.Secret)},
			Issuer:      issuer,
			Cfg: buildsSvc.Config{Namespace: c.Namespace, RegistryHost: c.RegistryHost,
				MaxArchiveBytes: c.MaxArchiveBytes, MaxConcurrent: c.MaxConcurrent, Deadline: c.Timeout},
			Logger: a.Logger,
		},
		Logger: a.Logger,
	}, nil
}

// registryPull : Secret de pull des conteneurs, côté worker.
func (a *App) registryPull() *workloadActs.RegistryPull {
	if !a.buildsEnabled() {
		return nil
	}
	return &workloadActs.RegistryPull{Host: a.Config.Builds.RegistryHost, Creds: registry.Credentials{Secret: []byte(a.Config.Builds.Secret)}}
}

// buildActivities : étapes du workflow, côté worker.
func (a *App) buildActivities() *buildActs.Activities {
	if !a.buildsEnabled() {
		return nil
	}
	c := a.Config.Builds.WithDefaults()
	opts := ""
	if c.RegistryInsecure {
		opts = ",registry.insecure=true"
	}
	acts := &buildActs.Activities{
		Repo:  buildsRepo.New(a.DB),
		Store: a.ObjectStore,
		K8s:   a.K8sProvider.Client,
		Job: buildActs.JobConfig{
			Namespace: c.Namespace, Image: c.BuildkitImage, RegistryOpts: opts,
			CPURequest: c.CPURequest, CPULimit: c.CPULimit, MemoryRequest: c.MemoryRequest, MemoryLimit: c.MemoryLimit,
			DiskLimit: c.DiskLimit, DeadlineSecond: int64(c.Timeout.Seconds()),
			RuntimeClass: c.RuntimeClass, NodeSelector: c.NodeSelector, EgressAllow: c.EgressAllowCIDRs,
		},
		Creds:      registry.Credentials{Secret: []byte(c.Secret)},
		RegHost:    c.RegistryHost,
		SourceBase: c.PublicAPIURL,
		Deployer:   a.WorkloadDeployer,
		Logger:     a.Logger,
	}
	if a.GitHub.Enabled() {
		acts.Git = a.GitHub
	}
	return acts
}

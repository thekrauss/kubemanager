package router

import (
	"github.com/thekrauss/kubemanager/internal/iam"
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	clustersCtrl "github.com/thekrauss/kubemanager/internal/modules/clusters"
	"github.com/thekrauss/kubemanager/internal/modules/iamapi"
	identityCtrl "github.com/thekrauss/kubemanager/internal/modules/identity"
	identitySvc "github.com/thekrauss/kubemanager/internal/modules/identity/service"
	projectCtrl "github.com/thekrauss/kubemanager/internal/modules/projects"
	projectSvc "github.com/thekrauss/kubemanager/internal/modules/projects/service"
	realtimeCtrl "github.com/thekrauss/kubemanager/internal/modules/realtime"
	"github.com/thekrauss/kubemanager/internal/modules/sites"
	sitesActs "github.com/thekrauss/kubemanager/internal/modules/sites/activities"
	sitesSvc "github.com/thekrauss/kubemanager/internal/modules/sites/service"
	workloadsCtrl "github.com/thekrauss/kubemanager/internal/modules/workloads"
	workloadsSvc "github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

func (a *App) initDomainLayers() error {
	a.Logger.Info("Initializing domain layers...")

	policies := &iam.PolicyService{DB: a.DB, Engine: a.IAM}
	rbacService := authSvc.NewRBACService(a.Repos.Auth, a.Logger)
	rbacService.Policies = policies
	apiKeyService := authSvc.NewAPIKeyService(
		a.Repos.Auth,
		authSvc.WithCache(a.AuthCache),
		authSvc.WithEnvTag(a.Config.ServiceName),
	)

	projectService := projectSvc.NewProjectService(
		a.TemporalDistributor,
		a.Config,
		a.Logger,
		a.Repos.Project,
		a.K8sProvider.Client,
		a.K8sProvider.Config, // fix #2 : GetMetrics a besoin du rest.Config
		a.Repos.Workload,     // fix #2 : GetTotalUsageByProject pour calcul usage réservé
	)
	workloadService := workloadsSvc.NewWorkloadService(
		a.TemporalDistributor,
		a.Repos.Workload,
		a.Repos.Project,
		a.K8sProvider.Client,
		a.K8sProvider.Config,
		a.Logger,
	)

	// Identity : dépend de Keycloak + AuthCache (state OAuth). Nil si Keycloak disabled.
	var identityService identitySvc.Service
	var identityController identityCtrl.Controller
	if a.Keycloak != nil {
		identityService = identitySvc.NewService(identitySvc.Deps{
			Keycloak:            a.Keycloak,
			Users:               a.Repos.Auth,
			Projects:            projectService,
			ProjectMembers:      a.Repos.Auth,
			OIDCClientID:        a.Config.Keycloak.ClientID,
			ActionEmailClientID: a.Config.Keycloak.ClientID,
			FrontendBaseURL:     a.Config.Frontend.BaseURL,
			SocialRedirectURI:   a.Config.Keycloak.SocialRedirectURI,
			Cache:               a.AuthCache,
			StateStore:          a.Cache, // Redis : cross-instance OK (fix H9)
		})
		identityController = identityCtrl.NewController(identityService, a.AuthCache)
	} else {
		a.Logger.Warnw("identity service skipped — keycloak not initialized")
	}

	rbacController := authCtrl.NewRBACController(rbacService)
	apiKeyController := authCtrl.NewAPIKeyController(apiKeyService)
	projectController := projectCtrl.NewProjectHandlers(projectService)
	workloadController := workloadsCtrl.NewWorkloadHandler(workloadService)

	realtimeController := realtimeCtrl.NewController(a.Config.Centrifugo, a.RealtimeSigner, a.IAM)

	clusterRepo := clustersCtrl.NewClusterRepository(a.DB)
	clustersService := clustersCtrl.NewService(a.Repos.Workload, clusterRepo, a.Publisher, a.Logger)
	clustersController := clustersCtrl.NewController(clustersService)

	// Sites statiques : actifs seulement si l'object store est configuré.
	var siteService sitesSvc.ISiteService
	var siteController sites.ISiteController
	if a.ObjectStore != nil {
		sc := a.Config.Sites
		siteService = sitesSvc.NewSiteService(a.Repos.Site, a.ObjectStore, a.TemporalDistributor, nil,
			sitesSvc.Config{
				BaseDomain: sc.BaseDomain,
				EdgeTarget: sc.EdgeTarget,
				Limits: sitesActs.Limits{
					MaxArchiveBytes:   sc.MaxArchiveBytes,
					MaxExtractedBytes: sc.MaxExtractedBytes,
					MaxFiles:          sc.MaxFiles,
				},
			}, a.Logger)
		siteController = sites.NewSiteController(siteService, a.Logger)
	}

	a.Services = &ServiceContainer{
		RBAC:     rbacService,
		APIKey:   apiKeyService,
		Project:  projectService,
		Workload: workloadService,
		Identity: identityService,
		Clusters: clustersService,
		Site:     siteService,
	}

	a.Controllers = &ControllerContainer{
		RBAC:     rbacController,
		APIKey:   apiKeyController,
		Project:  projectController,
		Workload: workloadController,
		Identity: identityController,
		Realtime: realtimeController,
		Clusters: clustersController,
		Site:     siteController,
		IAM:      iamapi.NewController(policies, a.IAM, a.ServiceAccounts, a.ClusterAgents),
	}

	a.Logger.Info("Domain layers successfully initialized.")
	return nil
}

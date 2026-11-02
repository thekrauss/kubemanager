package router

import (
	controller "github.com/thekrauss/kubemanager/internal/modules/auth"
	authRepos "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	"github.com/thekrauss/kubemanager/internal/modules/builds"
	clustersCtrl "github.com/thekrauss/kubemanager/internal/modules/clusters"
	"github.com/thekrauss/kubemanager/internal/modules/github"
	"github.com/thekrauss/kubemanager/internal/modules/iamapi"
	identity "github.com/thekrauss/kubemanager/internal/modules/identity"
	identitySvc "github.com/thekrauss/kubemanager/internal/modules/identity/service"
	"github.com/thekrauss/kubemanager/internal/modules/projects"
	projectRepos "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	projectSvc "github.com/thekrauss/kubemanager/internal/modules/projects/service"
	realtimeCtrl "github.com/thekrauss/kubemanager/internal/modules/realtime"
	"github.com/thekrauss/kubemanager/internal/modules/sites"
	sitesRepo "github.com/thekrauss/kubemanager/internal/modules/sites/repository"
	sitesSvc "github.com/thekrauss/kubemanager/internal/modules/sites/service"
	workload "github.com/thekrauss/kubemanager/internal/modules/workloads"
	workloadsRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	workloadsSvc "github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

type RepositoryContainer struct {
	Auth     authRepos.AuthRepository
	Project  projectRepos.ProjectRepository
	Workload workloadsRepo.WorkloadRepository
	Site     sitesRepo.SiteRepository
}

type ServiceContainer struct {
	RBAC     authSvc.IRBACService
	APIKey   authSvc.IAPIKeyService
	Project  projectSvc.IProjectService
	Workload workloadsSvc.IWorkloadService
	Identity identitySvc.Service
	Clusters clustersCtrl.IClusterService
	Site     sitesSvc.ISiteService // nil si sites désactivés
}

type ControllerContainer struct {
	APIKey   controller.IAPIKeyController
	RBAC     controller.IRBACController
	Project  projects.IProjectController
	Workload workload.IWorkloadController
	Identity identity.Controller
	// Auth : connexion, sessions, second facteur, emails (nil sans annuaire).
	Auth     *identity.AuthController
	Realtime realtimeCtrl.IRealtimeController
	Clusters clustersCtrl.IClusterController
	Site     sites.ISiteController // nil si sites désactivés
	IAM      *iamapi.Controller
	Builds   *builds.Controller // nil si builds désactivés
	GitHub   *github.Controller
}

func AddAllRoutes(a *App) {
	addIdentityRoutes(a)
	addRBACRoutes(a)
	addAPIKeyRoutes(a)
	addProjectRoutes(a)
	addWorkloadRoutes(a)
	addRealtimeRoutes(a)
	addClusterRoutes(a)
	addSiteRoutes(a)
	addBuildRoutes(a)
	addGitHubRoutes(a)
	addIAMRoutes(a)
}

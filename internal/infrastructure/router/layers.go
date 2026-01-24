package router

import (
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	projectCtrl "github.com/thekrauss/kubemanager/internal/modules/projects"
	projectRepos "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	projectSvc "github.com/thekrauss/kubemanager/internal/modules/projects/service"
	workloadsCtrl "github.com/thekrauss/kubemanager/internal/modules/workloads"
	workloadsRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	workloadsSvc "github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

func (a *App) initDomainLayers() error {
	a.Logger.Info(" domain layers...")

	hasher := &security.ConcretePasswordHasher{}

	apiKeyService := authSvc.NewAPIKeyService(a.Repos.Auth)
	rbacService := authSvc.NewRBACService(a.Repos.Auth, a.Logger)
	authService := authSvc.NewAuthService(a.Config, a.Repos.Auth, a.JWTManager, a.Cache, a.Logger, hasher)

	projectRepo := projectRepos.NewProjectRepository(a.DB)
	projectService := projectSvc.NewProjectService(a.TemporalClient, a.Config, a.Logger, projectRepo, a.K8sClient)

	workloadRepo := workloadsRepo.NewWorkloadRepository(a.DB)
	workloadService := workloadsSvc.NewWorkloadService(a.TemporalClient, workloadRepo, projectRepo)

	authController := authCtrl.NewAuthController(authService, rbacService)
	rbacController := authCtrl.NewRBACController(rbacService)
	apiKeyController := authCtrl.NewAPIKeyController(apiKeyService)
	projectController := projectCtrl.ProjectHandler{ProjectService: projectService}
	workloadController := workloadsCtrl.WorkloadController{WorkloadService: workloadService}

	a.Services = &ServiceContainer{
		Auth:     authService,
		RBAC:     rbacService,
		APIKey:   apiKeyService,
		Project:  projectService,
		Workload: workloadService,
	}

	a.Controllers = &ControllerContainer{
		Auth:     authController,
		RBAC:     rbacController,
		APIKey:   apiKeyController,
		Project:  &projectController,
		Workload: &workloadController,
	}

	a.Logger.Info("domain layers initialized.")
	return nil
}

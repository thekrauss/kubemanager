package router

import (
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	projectCtrl "github.com/thekrauss/kubemanager/internal/modules/projects"
	projectSvc "github.com/thekrauss/kubemanager/internal/modules/projects/service"
	workloadsCtrl "github.com/thekrauss/kubemanager/internal/modules/workloads"
	workloadsSvc "github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

func (a *App) initDomainLayers() error {
	a.Logger.Info("Initializing domain layers...")

	hasher := security.NewPasswordHasher()
	apiKeyService := authSvc.NewAPIKeyService(a.Repos.Auth)
	rbacService := authSvc.NewRBACService(a.Repos.Auth, a.Logger)
	authService := authSvc.NewAuthService(a.Config, a.Repos.Auth, a.Security.JWTManager, a.Cache, a.Logger, hasher)

	projectService := projectSvc.NewProjectService(a.Temporal.Client, a.Config, a.Logger, a.Repos.Project, a.K8sProvider.Client)
	workloadService := workloadsSvc.NewWorkloadService(a.Temporal.Client, a.Repos.Workload, a.Repos.Project)

	authController := authCtrl.NewAuthController(authService, rbacService)
	rbacController := authCtrl.NewRBACController(rbacService)
	apiKeyController := authCtrl.NewAPIKeyController(apiKeyService)
	projectController := projectCtrl.NewProjectHandlers(projectService)
	workloadController := workloadsCtrl.NewWorkloadHandler(workloadService)

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
		Project:  projectController,
		Workload: workloadController,
	}

	a.Logger.Info("Domain layers successfully initialized.")
	return nil
}

package router

import (
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	authRepos "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	projectCtrl "github.com/thekrauss/kubemanager/internal/modules/projects"
	projectRepos "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	projectSvc "github.com/thekrauss/kubemanager/internal/modules/projects/service"
	workloadsCtrl "github.com/thekrauss/kubemanager/internal/modules/workloads"
	workloadsRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	workloadsSvc "github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

type RepositoryContainer struct {
	Auth     authRepos.AuthRepository
	Project  projectRepos.ProjectRepository
	Workload workloadsRepo.WorkloadRepository
}

type ServiceContainer struct {
	Auth     *authSvc.AuthService
	RBAC     *authSvc.RBACService
	APIKey   *authSvc.APIKeyService
	Project  *projectSvc.ProjectService
	Workload *workloadsSvc.WorkloadService
}

type ControllerContainer struct {
	Auth     *authCtrl.AuthController
	RBAC     *authCtrl.RBACController
	APIKey   *authCtrl.APIKeyController
	Project  *projectCtrl.ProjectHandler
	Workload *workloadsCtrl.WorkloadController
}

func AddAllRoutes(a *App) {
	addAuthRoutes(a)
	addRBACRoutes(a)
	addAPIKeyRoutes(a)
	addProjectRoutes(a)
	addWorkloadRoutes(a)
}

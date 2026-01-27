package router

import (
	controller "github.com/thekrauss/kubemanager/internal/modules/auth"
	authRepos "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	"github.com/thekrauss/kubemanager/internal/modules/projects"
	projectRepos "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	projectSvc "github.com/thekrauss/kubemanager/internal/modules/projects/service"
	workload "github.com/thekrauss/kubemanager/internal/modules/workloads"
	workloadsRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	workloadsSvc "github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

type RepositoryContainer struct {
	Auth     authRepos.AuthRepository
	Project  projectRepos.ProjectRepository
	Workload workloadsRepo.WorkloadRepository
}

type ServiceContainer struct {
	Auth     authSvc.IAuthService
	RBAC     authSvc.IRBACService
	APIKey   authSvc.IAPIKeyService
	Project  projectSvc.IProjectService
	Workload *workloadsSvc.WorkloadService
}

type ControllerContainer struct {
	Auth     controller.IAuthController
	APIKey   controller.IAPIKeyController
	RBAC     controller.IRBACController
	Project  projects.IProjectController
	Workload workload.IWorkloadController
}

func AddAllRoutes(a *App) {
	addAuthRoutes(a)
	addRBACRoutes(a)
	addAPIKeyRoutes(a)
	addProjectRoutes(a)
	addWorkloadRoutes(a)
}

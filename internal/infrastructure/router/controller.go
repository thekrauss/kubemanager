package router

import (
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	authRepos "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	projectCtrl "github.com/thekrauss/kubemanager/internal/modules/projects"
	projectRepos "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	projectSvc "github.com/thekrauss/kubemanager/internal/modules/projects/service"
)

type RepositoryContainer struct {
	Auth    authRepos.AuthRepository
	Project projectRepos.ProjectRepository
}

type ServiceContainer struct {
	Auth    *authSvc.AuthService
	RBAC    *authSvc.RBACService
	APIKey  *authSvc.APIKeyService
	Project *projectSvc.ProjectService
}

type ControllerContainer struct {
	Auth    *authCtrl.AuthController
	RBAC    *authCtrl.RBACController
	APIKey  *authCtrl.APIKeyController
	Project *projectCtrl.ProjectHandler
}

func AddAllRoutes(a *App) {
	addAuthRoutes(a)
	addRBACRoutes(a)
	addAPIKeyRoutes(a)
	addProjectRoutes(a)
}

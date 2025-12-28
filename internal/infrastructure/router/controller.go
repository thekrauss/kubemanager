package router

import (
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	authRepos "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

type RepositoryContainer struct {
	Auth authRepos.AuthRepository
}

type ServiceContainer struct {
	Auth   *authSvc.AuthService
	RBAC   *authSvc.RBACService
	APIKey *authSvc.APIKeyService
}

type ControllerContainer struct {
	Auth   *authCtrl.AuthController
	RBAC   *authCtrl.RBACController
	APIKey *authCtrl.APIKeyController
}

func AddAllRoutes(a *App) {
	addAuthRoutes(a)
	addRBACRoutes(a)
	addAPIKeyRoutes(a)
}

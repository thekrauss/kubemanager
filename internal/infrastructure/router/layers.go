package router

import (
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

func (a *App) initDomainLayers() error {
	a.Logger.Info("Initializing domain layers...")

	hasher := &security.ConcretePasswordHasher{}
	apiKeyService := authSvc.NewAPIKeyService(a.Repos.Auth)
	rbacService := authSvc.NewRBACService(a.Repos.Auth, a.Logger)

	authService := authSvc.NewAuthService(
		a.Config,
		a.Repos.Auth,
		a.JWTManager,
		a.Cache,
		a.Logger,
		hasher,
	)
	authController := authCtrl.NewAuthController(authService, rbacService)
	rbacController := authCtrl.NewRBACController(rbacService)
	apiKeyController := authCtrl.NewAPIKeyController(apiKeyService)

	a.Services = &ServiceContainer{
		Auth:   authService,
		RBAC:   rbacService,
		APIKey: apiKeyService,
	}

	a.Controllers = &ControllerContainer{
		Auth:   authController,
		RBAC:   rbacController,
		APIKey: apiKeyController,
	}

	a.Logger.Info("All domain layers initialized.")
	return nil
}

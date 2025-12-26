package router

import (
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

func (a *App) initDomainLayers() error {
	a.Logger.Info("Initializing domain layers...")

	hasher := &security.ConcretePasswordHasher{}
	authRepo := repository.NewAuthRepository(a.DB)
	authService := authSvc.NewAuthService(a.Config, authRepo, a.JWTManager, a.Cache, a.Logger, hasher)
	authController := authCtrl.NewAuthController(authService)
	a.Services = &ServiceContainer{Auth: authService}

	a.Controllers = &ControllerContainer{
		Auth: authController,
	}

	a.Logger.Info("All domain layers initialized.")
	return nil
}

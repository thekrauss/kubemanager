package router

import "github.com/thekrauss/kubemanager/internal/modules/auth/repository"

func (a *App) initDomainLayers() error {
	a.Logger.Info("Initializing domain layers...")

	authRepo := repository.NewAuthRepository(a.DB)
	authUsecase := authUsecase.NewAuthUsecase(
		authRepo,
		a.JWTManager,
		a.Cache,
		a.Logger,
	)

	// Si tu as besoin d'instrumentation (metrics) comme dans ton exemple :
	// instrumentedAuthUsecase := authUsecase.NewAuthMetricMiddleware(authUsecase)

	// 3. ROUTES / CONTROLLERS (Niveau Transport/API)
	authRoutes := auth.NewAuthRoutes(authUsecase)
	// projectRoutes := projects.NewRoutes(projectUsecase)

	// 4. MAPPING DES CONTROLLERS DANS L'APP
	// On remplit la struct Controllers que tu as définie
	a.Controllers = &ControllerContainer{
		AuthRoutes: authRoutes,
	}

	a.Logger.Info("All domain layers initialized.")
	return nil
}

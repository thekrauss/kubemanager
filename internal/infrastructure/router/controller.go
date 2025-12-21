package router

type ServiceContainer struct {
	Auth *authSvc.AuthService
}

type ControllerContainer struct {
	Auth *authCtrl.AuthController
}

func AddAllRoutes(a *App) {
}

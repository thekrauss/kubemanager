package router

import (
	authCtrl "github.com/thekrauss/kubemanager/internal/modules/auth"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
)

type ServiceContainer struct {
	Auth *authSvc.AuthService
}

type ControllerContainer struct {
	Auth *authCtrl.AuthController
}

func AddAllRoutes(a *App) {
	addAuthRoutes(a)
}

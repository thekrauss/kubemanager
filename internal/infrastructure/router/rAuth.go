package router

import (
	"net/http"

	"github.com/loopfz/gadgeto/tonic"
)

var (
	AuthGroup = RootGroup.NewGroup("/auth", "Gestion de l'authentification et des tokens")
)

func addAuthRoutes(app *App) {
	r := app.Controllers.Auth

	AuthGroup.AddRoute("/register", http.MethodPost, "Inscription", tonic.Handler(r.Register, http.StatusCreated))
	AuthGroup.AddRoute("/login", http.MethodPost, "Connexion", tonic.Handler(r.Login, http.StatusOK))
	AuthGroup.AddRoute("/validate", http.MethodPost, "Validation", tonic.Handler(r.ValidateToken, http.StatusOK))
	AuthGroup.AddRoute("/refresh", http.MethodPost, "Refresh Token", tonic.Handler(r.RefreshToken, http.StatusOK))
	AuthGroup.AddRoute("/forgot-password", http.MethodPost, "Forgot Password", tonic.Handler(r.ForgotPassword, http.StatusOK))
	AuthGroup.AddRoute("/reset-password", http.MethodPost, "Reset Password", tonic.Handler(r.ResetPassword, http.StatusOK))

}

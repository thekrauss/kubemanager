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

	AuthGroup.AddRoute("/login", http.MethodPost, "Connexion utilisateur", tonic.Handler(r.Login, http.StatusOK))
	AuthGroup.AddRoute("/validate", http.MethodPost, "Validation du token", tonic.Handler(r.ValidateToken, http.StatusOK))

}

package router

import (
	"net/http"

	"github.com/loopfz/gadgeto/tonic"
)

var (
	AuthGroup = RootGroup.NewGroup("/auth", "Gestion de l'authentification et des tokens")

	RBACGroup    = RootGroup.NewGroup("/rbac", "Gestion des rôles et permissions")
	ProjectGroup = RootGroup.NewGroup("/projects", "Gestion des projets et membres")

	APIKeyGroup = RootGroup.NewGroup("/users/api-keys", "Gestion des clés API utilisateur")
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

func addRBACRoutes(app *App) {
	r := app.Controllers.RBAC

	RBACGroup.AddRoute("/roles", http.MethodGet, "Lister tous les rôles disponibles", tonic.Handler(r.ListAllRoles, http.StatusOK))
	ProjectGroup.AddRoute("/assign-role", http.MethodPost, "Assigner un rôle à un utilisateur", tonic.Handler(r.AssignRole, http.StatusOK))
	ProjectGroup.AddRoute("/:projectID/members", http.MethodGet, "Lister les membres d'un projet", tonic.Handler(r.ListProjectMembers, http.StatusOK))
	ProjectGroup.AddRoute("/:projectID/members/:userID", http.MethodDelete, "Révoquer l'accès d'un membre", tonic.Handler(r.RevokeProjectAccess, http.StatusOK))
}

func addAPIKeyRoutes(app *App) {
	ctrl := app.Controllers.APIKey

	APIKeyGroup.AddRoute("", http.MethodGet, "Lister les clés API", tonic.Handler(ctrl.ListKeys, http.StatusOK))
	APIKeyGroup.AddRoute("", http.MethodPost, "Créer une nouvelle clé API", tonic.Handler(ctrl.CreateKey, http.StatusCreated))
	APIKeyGroup.AddRoute("/:id", http.MethodDelete, "Révoquer une clé API", tonic.Handler(ctrl.RevokeKey, http.StatusOK))
}

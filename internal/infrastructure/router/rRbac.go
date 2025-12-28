package router

import (
	"net/http"

	"github.com/loopfz/gadgeto/tonic"
)

var (
	RBACGroup    = RootGroup.NewGroup("/rbac", "Gestion des rôles et permissions")
	ProjectGroup = RootGroup.NewGroup("/projects", "Gestion des projets et membres")
)

func addRBACRoutes(app *App) {
	r := app.Controllers.RBAC

	RBACGroup.AddRoute("/roles", http.MethodGet, "Lister tous les rôles disponibles", tonic.Handler(r.ListAllRoles, http.StatusOK))
	ProjectGroup.AddRoute("/assign-role", http.MethodPost, "Assigner un rôle à un utilisateur", tonic.Handler(r.AssignRole, http.StatusOK))
	ProjectGroup.AddRoute("/:projectID/members", http.MethodGet, "Lister les membres d'un projet", tonic.Handler(r.ListProjectMembers, http.StatusOK))
	ProjectGroup.AddRoute("/:projectID/members/:userID", http.MethodDelete, "Révoquer l'accès d'un membre", tonic.Handler(r.RevokeProjectAccess, http.StatusOK))
}

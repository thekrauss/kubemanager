package router

import (
	"net/http"

	"github.com/loopfz/gadgeto/tonic"
)

var (
	AuthGroup     = RootGroup.NewGroup("/auth", "Gestion de l'authentification et des tokens")
	RBACGroup     = RootGroup.NewGroup("/rbac", "Configuration globale des rôles")
	ProjectGroup  = RootGroup.NewGroup("/projects", "Gestion des projets et de leurs membres")
	APIKeyGroup   = RootGroup.NewGroup("/users/api-keys", "Gestion des clés API utilisateur")
	WorkloadGroup = RootGroup.NewGroup("/workloads", "Gestion des déploiements Helm (Workloads)")
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
	RBACGroup.AddRoute("/assign-role", http.MethodPost, "Assigner un rôle à un utilisateur", tonic.Handler(r.AssignRole, http.StatusOK))
	RBACGroup.AddRoute("/:projectID/members", http.MethodGet, "Lister les membres d'un projet", tonic.Handler(r.ListProjectMembers, http.StatusOK))
	RBACGroup.AddRoute("/:projectID/members/:userID", http.MethodDelete, "Révoquer l'accès d'un membre", tonic.Handler(r.RevokeProjectAccess, http.StatusOK))
}

func addAPIKeyRoutes(app *App) {
	r := app.Controllers.APIKey
	APIKeyGroup.AddRoute("", http.MethodGet, "Lister les clés API", tonic.Handler(r.ListKeys, http.StatusOK))
	APIKeyGroup.AddRoute("", http.MethodPost, "Créer une nouvelle clé API", tonic.Handler(r.CreateKey, http.StatusCreated))
	APIKeyGroup.AddRoute("/:id", http.MethodDelete, "Révoquer une clé API", tonic.Handler(r.RevokeKey, http.StatusOK))
}

func addProjectRoutes(app *App) {
	r := app.Controllers.Project
	//ProjectGroup.AddRoute("", http.MethodGet, "Lister mes projets", tonic.Handler(r.ListProjects, http.StatusOK))
	ProjectGroup.AddRoute("", http.MethodPost, "Créer un projet", tonic.Handler(r.CreateProject, http.StatusCreated))
	//ProjectGroup.AddRoute("/:id", http.MethodGet, "Détails d'un projet", tonic.Handler(r.GetProject, http.StatusOK))
	ProjectGroup.AddRoute("/:id/status", http.MethodGet, "Statut K8s d'un projet", tonic.Handler(r.GetProjectStatus, http.StatusOK))
	ProjectGroup.AddRoute("/:id/metrics", http.MethodGet, "Métriques de consommation", tonic.Handler(r.GetProjectMetrics, http.StatusOK))
	ProjectGroup.AddRoute("/:id", http.MethodDelete, "Supprimer un projet", tonic.Handler(r.DeleteProject, http.StatusAccepted))
}

func addWorkloadRoutes(app *App) {
	r := app.Controllers.Workload

	//WorkloadGroup.AddRoute("", http.MethodGet, "Lister tous les workloads", tonic.Handler(r.ListWorkloads, http.StatusOK))
	WorkloadGroup.AddRoute("/:id", http.MethodGet, "Statut détaillé d'un workload", tonic.Handler(r.GetWorkloadStatus, http.StatusOK))

	WorkloadGroup.AddRoute("", http.MethodPost, "Déployer un nouveau workload", tonic.Handler(r.CreateWorkload, http.StatusAccepted))
	//WorkloadGroup.AddRoute("/:id", http.MethodPut, "Mettre à jour un workload (Scaling/Image)", tonic.Handler(r.UpdateWorkload, http.StatusAccepted))
	//WorkloadGroup.AddRoute("/:id", http.MethodDelete, "Supprimer et désinstaller un workload", tonic.Handler(r.DeleteWorkload, http.StatusAccepted))

	//WorkloadGroup.AddRoute("/:id/logs", http.MethodGet, "Récupérer les logs des pods", tonic.Handler(r.GetWorkloadLogs, http.StatusOK))
}

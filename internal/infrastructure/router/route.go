package router

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/loopfz/gadgeto/tonic"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	workloadsRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
)

// Chaque route déclare son accès (Public, Authenticated, Perm, ClusterAgent) :
// voir access.go et docs/architecture/iam.md §5.

var (
	IdentityGroup = RootGroup.NewGroup("/identity", "Authentification Keycloak et bootstrap utilisateur")
	RBACGroup     = RootGroup.NewGroup("/rbac", "Configuration globale des rôles")
	ProjectGroup  = RootGroup.NewGroup("/projects", "Gestion des projets et de leurs membres")
	APIKeyGroup   = RootGroup.NewGroup("/users/api-keys", "Gestion des clés API utilisateur")
	WorkloadGroup = RootGroup.NewGroup("/workloads", "Gestion des déploiements Helm (Workloads)")
	RealtimeGroup = RootGroup.NewGroup("/realtime", "Tokens Centrifugo (WebSocket events)")
	ClustersGroup = RootGroup.NewGroup("/clusters", "API watcher (pod events depuis chaque cluster K3s géré)")
)

func addClusterRoutes(app *App) {
	r := app.Controllers.Clusters
	if r == nil {
		return
	}
	g := newGuards(app)
	// La clé d'un cluster ne vaut que pour ce cluster.
	watcher := g.ClusterAgent(security.ClusterParam("clusterID"))

	// Signatures EXACT respectées pour que le watcher existant marche sans modif.
	ClustersGroup.AddRoute("/ClusterService/Enroll", http.MethodPost,
		"Enroll : enregistre un cluster K3s au démarrage du watcher",
		g.ClusterAgent(security.ClusterJSONBody("cluster_id")), tonic.Handler(r.Enroll, http.StatusOK))
	ClustersGroup.AddRoute("/ConfigService/:clusterID/Get", http.MethodGet,
		"GetConfig : retourne dedup/batch/enabled_reasons au watcher",
		watcher, tonic.Handler(r.GetConfig, http.StatusOK))
	ClustersGroup.AddRoute("/ProjectService/:clusterID/onPodEvent", http.MethodPost,
		"onPodEvent : reçoit un event pod (CrashLoopBackOff, OOMKilled...) → DB + Centrifugo",
		watcher, tonic.Handler(r.OnPodEvent, http.StatusOK))

	adminClusters := RootGroup.NewGroup("/admin/clusters", "Admin — registry des clusters enrollés")
	adminClusters.AddRoute("", http.MethodGet,
		"Liste les clusters enrollés (ClusterID, Version, LastSeenAt, EnrolledAt)",
		g.Perm(iam.PlatformClustersList, security.PlatformResource), tonic.Handler(r.ListClusters, http.StatusOK))
}

func addRealtimeRoutes(app *App) {
	r := app.Controllers.Realtime
	if r == nil {
		return
	}
	RealtimeGroup.AddRoute("/token", http.MethodGet, "Connection token JWT (ouverture WebSocket)",
		Authenticated(), tonic.Handler(r.GetConnectionToken, http.StatusOK))
	// Le projet est dans le nom du canal : le contrôleur vérifie
	// resourcemanager.projects.get via l'IAM avant de signer.
	RealtimeGroup.AddRoute("/subscribe", http.MethodGet, "Subscription token scopé à un canal (vérifie authz projet)",
		Authenticated(), tonic.Handler(r.GetSubscriptionToken, http.StatusOK))
}

func addIdentityRoutes(app *App) {
	r := app.Controllers.Identity
	if r == nil {
		return
	}
	IdentityGroup.AddRoute("/register", http.MethodPost, "Créer un compte Keycloak", Public(), tonic.Handler(r.Register, http.StatusCreated))
	IdentityGroup.AddRoute("/login", http.MethodPost, "Connexion email/password (ROPC)", Public(), tonic.Handler(r.Login, http.StatusOK))
	IdentityGroup.AddRoute("/refresh", http.MethodPost, "Renouveler les tokens via refresh_token", Public(), tonic.Handler(r.RefreshToken, http.StatusOK))
	IdentityGroup.AddRoute("/forgot-password", http.MethodPost, "Envoyer un email de reset password", Public(), tonic.Handler(r.ForgotPassword, http.StatusAccepted))
	IdentityGroup.AddRoute("/resend-verification", http.MethodPost, "Renvoyer l'email de vérification (par email)", Public(), tonic.Handler(r.ResendVerificationEmailForEmail, http.StatusAccepted))

	// Login hébergé Keycloak (Authorization Code + PKCE). Le callback partage
	// le handler du social (state + verifier en Redis).
	IdentityGroup.AddRoute("/oidc/authorize", http.MethodGet, "URL de la page de login Keycloak (PKCE S256)", Public(), tonic.Handler(r.LoginURL, http.StatusOK))
	IdentityGroup.AddRoute("/oidc/callback", http.MethodGet, "Callback login hébergé (échange code + verifier → tokens)", Public(), tonic.Handler(r.SocialCallback, http.StatusOK))
	IdentityGroup.AddRoute("/social/:provider", http.MethodGet, "URL d'autorisation sociale (kc_idp_hint)", Public(), tonic.Handler(r.SocialLoginURL, http.StatusOK))
	IdentityGroup.AddRoute("/social/callback", http.MethodGet, "Callback OAuth2 (échange code → tokens)", Public(), tonic.Handler(r.SocialCallback, http.StatusOK))

	IdentityGroup.AddRoute("/logout", http.MethodPost, "Déconnexion (révoque refresh_token + blacklist JTI)", Authenticated(), tonic.Handler(r.Logout, http.StatusOK))
	IdentityGroup.AddRoute("/validate", http.MethodGet, "Valider le token courant", Authenticated(), tonic.Handler(r.ValidateToken, http.StatusOK))
	IdentityGroup.AddRoute("/change-password", http.MethodPost, "Changer le mot de passe courant", Authenticated(), tonic.Handler(r.ChangePassword, http.StatusOK))
	IdentityGroup.AddRoute("/bootstrap", http.MethodPost, "Provisionner le profil local + projet initial", Authenticated(), tonic.Handler(r.Bootstrap, http.StatusOK))
	IdentityGroup.AddRoute("/bootstrap/status", http.MethodGet, "État du bootstrap local", Authenticated(), tonic.Handler(r.BootstrapStatus, http.StatusOK))
}

func addRBACRoutes(app *App) {
	r := app.Controllers.RBAC
	g := newGuards(app)

	RBACGroup.AddRoute("/roles", http.MethodGet, "Lister tous les rôles disponibles",
		Authenticated(), tonic.Handler(r.ListAllRoles, http.StatusOK))
	RBACGroup.AddRoute("/assign-role", http.MethodPost, "Assigner un rôle à un utilisateur",
		g.Perm(iam.ProjectsSetIamPolicy, security.ProjectJSONBody("project_id")), tonic.Handler(r.AssignRole, http.StatusOK))
	RBACGroup.AddRoute("/:projectID/members", http.MethodGet, "Lister les membres d'un projet",
		g.Perm(iam.ProjectsGetIamPolicy, security.ProjectParam("projectID")), tonic.Handler(r.ListProjectMembers, http.StatusOK))
	RBACGroup.AddRoute("/:projectID/members/:userID", http.MethodDelete, "Révoquer l'accès d'un membre",
		g.Perm(iam.ProjectsSetIamPolicy, security.ProjectParam("projectID")), tonic.Handler(r.RevokeProjectAccess, http.StatusOK))
}

func addAPIKeyRoutes(app *App) {
	r := app.Controllers.APIKey

	// Clés de l'appelant uniquement (le service filtre par userID).
	APIKeyGroup.AddRoute("", http.MethodGet, "Lister les clés API", Authenticated(), tonic.Handler(r.ListKeys, http.StatusOK))
	APIKeyGroup.AddRoute("", http.MethodPost, "Créer une nouvelle clé API", Authenticated(), tonic.Handler(r.CreateKey, http.StatusCreated))
	APIKeyGroup.AddRoute("/audit", http.MethodGet, "Vue audit des clés API (usage, status, last_used_at)", Authenticated(), tonic.Handler(r.ListAudit, http.StatusOK))
	APIKeyGroup.AddRoute("/:id", http.MethodGet, "Introspection d'une clé API", Authenticated(), tonic.Handler(r.GetKey, http.StatusOK))
	APIKeyGroup.AddRoute("/:id", http.MethodDelete, "Révoquer une clé API", Authenticated(), tonic.Handler(r.RevokeKey, http.StatusOK))

}

func addProjectRoutes(app *App) {
	r := app.Controllers.Project
	g := newGuards(app)
	project := security.ProjectParam("id")

	// Création dans l'organisation personnelle de l'appelant, qui en devient owner.
	ProjectGroup.AddRoute("", http.MethodPost, "Créer un projet", Authenticated(), tonic.Handler(r.CreateProject, http.StatusCreated))
	ProjectGroup.AddRoute("/:id/status", http.MethodGet, "Statut K8s d'un projet",
		g.Perm(iam.ProjectsGet, project), tonic.Handler(r.GetProjectStatus, http.StatusOK))
	ProjectGroup.AddRoute("/:id/metrics", http.MethodGet, "Métriques de consommation",
		g.Perm(iam.ProjectsGet, project), tonic.Handler(r.GetProjectMetrics, http.StatusOK))
	ProjectGroup.AddRoute("/:id", http.MethodDelete, "Supprimer un projet",
		g.Perm(iam.ProjectsDelete, project), tonic.Handler(r.DeleteProject, http.StatusAccepted))
}

func addWorkloadRoutes(app *App) {
	r := app.Controllers.Workload
	g := newGuards(app)
	workload := workloadResource(app.Repos.Workload)
	on := func(p iam.Permission) Access { return g.Perm(p, workload) }

	WorkloadGroup.AddRoute("", http.MethodPost, "Déployer un nouveau workload",
		g.Perm(iam.WorkloadsCreate, security.ProjectJSONBody("project_id")), tonic.Handler(r.CreateWorkload, http.StatusAccepted))
	WorkloadGroup.AddRoute("", http.MethodGet, "Lister les workloads d'un projet",
		g.Perm(iam.WorkloadsList, security.ProjectQuery("project_id")), tonic.Handler(r.ListWorkloads, http.StatusOK))
	WorkloadGroup.AddRoute("/:id", http.MethodGet, "Détails d'un workload (état DB)", on(iam.WorkloadsGet), tonic.Handler(r.GetWorkload, http.StatusOK))
	WorkloadGroup.AddRoute("/:id", http.MethodPatch, "Mettre à jour un workload (image, env, storage)", on(iam.WorkloadsUpdate), tonic.Handler(r.UpdateWorkload, http.StatusAccepted))
	WorkloadGroup.AddRoute("/:id", http.MethodDelete, "Supprimer un workload (Helm uninstall + DB)", on(iam.WorkloadsDelete), tonic.Handler(r.DeleteWorkload, http.StatusAccepted))

	WorkloadGroup.AddRoute("/:id/stop", http.MethodPost, "Stop (scale to 0)", on(iam.WorkloadsStop), tonic.Handler(r.StopWorkload, http.StatusOK))
	WorkloadGroup.AddRoute("/:id/start", http.MethodPost, "Start (scale back to N)", on(iam.WorkloadsStart), tonic.Handler(r.StartWorkload, http.StatusOK))
	WorkloadGroup.AddRoute("/:id/restart", http.MethodPost, "Restart (rollout restart)", on(iam.WorkloadsRestart), tonic.Handler(r.RestartWorkload, http.StatusOK))
	WorkloadGroup.AddRoute("/:id/rollback", http.MethodPost, "Rollback à une révision Helm", on(iam.WorkloadsRollback), tonic.Handler(r.RollbackWorkload, http.StatusOK))

	WorkloadGroup.AddRoute("/:id/status", http.MethodGet, "Statut live (DB + K8s runtime)", on(iam.WorkloadsGet), tonic.Handler(r.GetWorkloadStatus, http.StatusOK))
	WorkloadGroup.AddRoute("/:id/events", http.MethodGet, "Events K8s du workload", on(iam.WorkloadsGet), tonic.Handler(r.GetWorkloadEvents, http.StatusOK))
	WorkloadGroup.AddRoute("/:id/metrics", http.MethodGet, "Metrics CPU/RAM live (metrics-server) + reserved (DB)", on(iam.WorkloadMetrics), tonic.Handler(r.GetWorkloadMetrics, http.StatusOK))
	// Logs : streaming direct sur c.Writer (pas tonic — on contrôle la réponse).
	WorkloadGroup.AddRoute("/:id/logs", http.MethodGet, "Stream logs du pod (chunked HTTP)", on(iam.WorkloadLogsRead), wrapRawHandler(r.GetWorkloadLogs))
}

// workloadResource : "projects/{p}/workloads/{id}" pour le workload ":id".
func workloadResource(repo workloadsRepo.WorkloadRepository) security.ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			return "", security.ErrResourceRefInvalid
		}
		w, err := repo.GetByID(c.Request.Context(), id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", security.ErrResourceNotFound
		}
		if err != nil {
			return "", err
		}
		return iam.ProjectName(w.ProjectID).Child("workloads", id), nil
	}
}

// addSiteRoutes : sites statiques, sous le projet (":id" = projet).
func addSiteRoutes(app *App) {
	r := app.Controllers.Site
	if r == nil {
		return
	}
	g := newGuards(app)
	sites := RootGroup.NewGroup("/projects/:id/sites", "Sites statiques")
	project := security.ProjectParam("id")
	site := security.ProjectChild("id", "sites", "siteID")
	on := func(p iam.Permission) Access { return g.Perm(p, site) }

	sites.AddRoute("", http.MethodGet, "Lister les sites du projet", g.Perm(iam.SitesList, project), tonic.Handler(r.ListSites, http.StatusOK))
	sites.AddRoute("", http.MethodPost, "Créer un site", g.Perm(iam.SitesCreate, project), tonic.Handler(r.CreateSite, http.StatusCreated))
	sites.AddRoute("/:siteID", http.MethodGet, "Détails d'un site", on(iam.SitesGet), tonic.Handler(r.GetSite, http.StatusOK))
	sites.AddRoute("/:siteID", http.MethodPatch, "Modifier un site (mode SPA)", on(iam.SitesUpdate), tonic.Handler(r.UpdateSite, http.StatusOK))
	sites.AddRoute("/:siteID", http.MethodDelete, "Supprimer un site (fichiers + domaines)", on(iam.SitesDelete), tonic.Handler(r.DeleteSite, http.StatusAccepted))

	sites.AddRoute("/:siteID/deployments", http.MethodGet, "Historique des déploiements", on(iam.SiteDeploymentsList), tonic.Handler(r.ListDeployments, http.StatusOK))
	// Upload multipart (champ "archive") : hors tonic.
	sites.AddRoute("/:siteID/deployments", http.MethodPost, "Déployer une archive .zip (multipart, champ archive)", on(iam.SiteDeploymentsCreate), wrapRawHandler(r.UploadDeployment))
	sites.AddRoute("/:siteID/deployments/:deploymentID/activate", http.MethodPost, "Activer un déploiement (rollback)", on(iam.SiteDeploymentsActive), tonic.Handler(r.ActivateDeployment, http.StatusOK))

	sites.AddRoute("/:siteID/domains", http.MethodGet, "Domaines personnalisés du site", on(iam.SiteDomainsList), tonic.Handler(r.ListDomains, http.StatusOK))
	sites.AddRoute("/:siteID/domains", http.MethodPost, "Ajouter un domaine personnalisé", on(iam.SiteDomainsCreate), tonic.Handler(r.AddDomain, http.StatusCreated))
	sites.AddRoute("/:siteID/domains/:domainID/verify", http.MethodPost, "Vérifier le DNS d'un domaine", on(iam.SiteDomainsVerify), tonic.Handler(r.VerifyDomain, http.StatusOK))
	sites.AddRoute("/:siteID/domains/:domainID", http.MethodDelete, "Retirer un domaine personnalisé", on(iam.SiteDomainsDelete), tonic.Handler(r.DeleteDomain, http.StatusNoContent))
}

// addIAMRoutes : API publique de l'IAM (docs/architecture/iam.md §14).
func addIAMRoutes(app *App) {
	r := app.Controllers.IAM
	if r == nil {
		return
	}
	g := newGuards(app)
	grp := RootGroup.NewGroup("/iam", "IAM : policies, permissions, rôles")

	grp.AddRoute("/policy", http.MethodGet, "Lire la policy d'une ressource (bindings, etag, membres)",
		g.PermFor(iam.GetPolicyPermission, security.ResourceQuery("resource")), tonic.Handler(r.GetPolicy, http.StatusOK))
	grp.AddRoute("/policy", http.MethodPost, "Remplacer la policy d'une ressource (etag obligatoire)",
		g.PermFor(iam.SetPolicyPermission, security.ResourceJSONBody("resource")), tonic.Handler(r.SetPolicy, http.StatusOK))
	grp.AddRoute("/testPermissions", http.MethodPost, "Permissions de l'appelant sur une ressource (affichage des actions)",
		Authenticated(), tonic.Handler(r.TestPermissions, http.StatusOK))
	grp.AddRoute("/roles", http.MethodGet, "Rôles prédéfinis attribuables",
		Authenticated(), tonic.Handler(r.ListRoles, http.StatusOK))
	grp.AddRoute("/permissions", http.MethodGet, "Catalogue des permissions",
		Authenticated(), tonic.Handler(r.ListPermissions, http.StatusOK))

	grp.AddRoute("/auditLogs", http.MethodGet, "Journal d'audit d'une ressource (et de ses descendantes)",
		g.Perm(iam.AuditLogsRead, security.ResourceQuery("resource")), tonic.Handler(r.ListAuditLogs, http.StatusOK))

	// Clés des watchers : comptes de service système des clusters (staff).
	ck := RootGroup.NewGroup("/admin/cluster-keys", "Admin — clés des watchers (comptes de service système des clusters)")
	ck.AddRoute("", http.MethodPost, "Créer une clé de watcher (crée le compte système du cluster au besoin ; secret affiché une seule fois)",
		g.Perm(iam.PlatformClusterKeysCreate, security.PlatformResource), tonic.Handler(r.CreateClusterKey, http.StatusCreated))
	ck.AddRoute("", http.MethodGet, "Lister les clés de watcher, v2 et legacy (active|expiring|expired|revoked)",
		g.Perm(iam.PlatformClusterKeysList, security.PlatformResource), tonic.Handler(r.ListClusterKeys, http.StatusOK))
	ck.AddRoute("/:keyID/rotate", http.MethodPost, "Rotation sans coupure : nouvelle clé, l'ancienne expire après la période de grâce",
		g.Perm(iam.PlatformClusterKeysRotate, security.PlatformResource), tonic.Handler(r.RotateClusterKey, http.StatusCreated))
	ck.AddRoute("/:keyID", http.MethodDelete, "Révoquer une clé de watcher (effet immédiat)",
		g.Perm(iam.PlatformClusterKeysRevoke, security.PlatformResource), tonic.Handler(r.RevokeClusterKey, http.StatusNoContent))

	// Comptes de service et clés API v2, sous le projet.
	sas := RootGroup.NewGroup("/projects/:id/serviceAccounts", "Comptes de service et clés API")
	project := security.ProjectParam("id")
	sa := security.ProjectChild("id", "serviceAccounts", "saID")
	sas.AddRoute("", http.MethodGet, "Lister les comptes de service", g.Perm(iam.ServiceAccountsList, project), tonic.Handler(r.ListServiceAccounts, http.StatusOK))
	sas.AddRoute("", http.MethodPost, "Créer un compte de service (sans rôle)", g.Perm(iam.ServiceAccountsCreate, project), tonic.Handler(r.CreateServiceAccount, http.StatusCreated))
	sas.AddRoute("/:saID", http.MethodGet, "Détails d'un compte de service", g.Perm(iam.ServiceAccountsGet, sa), tonic.Handler(r.GetServiceAccount, http.StatusOK))
	sas.AddRoute("/:saID", http.MethodDelete, "Supprimer un compte de service (clés et rôles compris)", g.Perm(iam.ServiceAccountsDelete, sa), tonic.Handler(r.DeleteServiceAccount, http.StatusNoContent))
	sas.AddRoute("/:saID/apiKeys", http.MethodGet, "Lister les clés API du compte", g.Perm(iam.APIKeysList, sa), tonic.Handler(r.ListAPIKeys, http.StatusOK))
	sas.AddRoute("/:saID/apiKeys", http.MethodPost, "Créer une clé API (secret affiché une seule fois ; actAs requis)", g.Perm(iam.APIKeysCreate, sa), tonic.Handler(r.CreateAPIKey, http.StatusCreated))
	sas.AddRoute("/:saID/apiKeys/:keyID", http.MethodDelete, "Révoquer une clé API (effet immédiat)", g.Perm(iam.APIKeysDelete, sa), tonic.Handler(r.RevokeAPIKey, http.StatusNoContent))
}

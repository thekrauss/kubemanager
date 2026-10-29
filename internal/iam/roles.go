package iam

import (
	"sort"
	"strings"
)

// Role : ensemble nommé de permissions. Les rôles prédéfinis vivent dans le
// code (versionnés avec lui) ; les rôles personnalisés vivront en DB (phase B).
type Role struct {
	Name        string       `json:"name"` // "roles/editor"
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Permissions []Permission `json:"permissions"`
	// PlatformOnly : rôle du staff betorun, attribuable seulement sur "platform".
	PlatformOnly bool `json:"-"`
}

func (r Role) has(p Permission) bool {
	for _, x := range r.Permissions {
		if x == p {
			return true
		}
	}
	return false
}

var (
	viewerPerms = []Permission{
		OrganizationsGet, OrganizationsGetIamPolicy,
		ProjectsGet, ProjectsList, ProjectsGetIamPolicy,
		WorkloadsList, WorkloadsGet, WorkloadLogsRead, WorkloadMetrics,
		SitesList, SitesGet, SiteDeploymentsList, SiteDomainsList,
		ServiceAccountsList, ServiceAccountsGet, ServiceAccountKeysList, APIKeysList,
		RolesList,
	}

	// developerPerms : parité exacte avec l'ancien rôle DEVELOPER (création et
	// cycle de vie, sans suppression) pour qu'aucune migration n'élargisse un droit.
	developerPerms = concat(viewerPerms, []Permission{
		WorkloadsCreate, WorkloadsUpdate, WorkloadsStart, WorkloadsStop,
		WorkloadsRestart, WorkloadsRollback,
		SitesCreate, SitesUpdate, SiteDeploymentsCreate, SiteDeploymentsActive,
		SiteDomainsCreate, SiteDomainsVerify,
		ServiceAccountsActAs,
	})

	editorPerms = concat(developerPerms, []Permission{
		ProjectsUpdate,
		WorkloadsDelete, SitesDelete, SiteDomainsDelete,
		ServiceAccountsCreate, ServiceAccountsUpdate, ServiceAccountsDelete,
		ServiceAccountKeysCreate, ServiceAccountKeysDelete,
		APIKeysCreate, APIKeysDelete,
	})

	ownerPerms = concat(editorPerms, []Permission{
		OrganizationsUpdate, OrganizationsSetIamPolicy,
		ProjectsCreate, ProjectsDelete, ProjectsSetIamPolicy,
		RolesCreate, RolesUpdate, RolesDelete,
		BillingAccountsGet, BillingAccountsUpdate, BillingPaymentsCreate,
		AuditLogsRead,
	})

	platformAdminPerms = []Permission{
		PlatformClustersList, PlatformClusterKeysList, PlatformClusterKeysCreate, PlatformClusterKeysRotate,
	}
)

var predefinedRoles = []Role{
	{Name: "roles/owner", Title: "Propriétaire",
		Description: "Accès complet, y compris les accès, la facturation et la suppression.",
		Permissions: ownerPerms},
	{Name: "roles/editor", Title: "Éditeur",
		Description: "Crée, modifie et supprime les ressources ; ne gère ni les accès ni la facturation.",
		Permissions: editorPerms},
	{Name: "roles/developer", Title: "Développeur",
		Description: "Déploie et opère les conteneurs et les sites, sans rien supprimer.",
		Permissions: developerPerms},
	{Name: "roles/viewer", Title: "Lecteur",
		Description: "Consulte les ressources, les logs et les métriques.",
		Permissions: viewerPerms},

	{Name: "roles/workloads.developer", Title: "Développeur conteneurs",
		Description: "Déploie et opère les conteneurs du projet.",
		Permissions: []Permission{ProjectsGet, WorkloadsList, WorkloadsGet, WorkloadsCreate, WorkloadsUpdate,
			WorkloadsStart, WorkloadsStop, WorkloadsRestart, WorkloadsRollback, WorkloadLogsRead, WorkloadMetrics,
			ServiceAccountsActAs}},
	{Name: "roles/workloads.viewer", Title: "Lecteur conteneurs",
		Description: "Consulte les conteneurs, leurs logs et leurs métriques.",
		Permissions: []Permission{ProjectsGet, WorkloadsList, WorkloadsGet, WorkloadLogsRead, WorkloadMetrics}},
	{Name: "roles/sites.admin", Title: "Administrateur sites",
		Description: "Gère entièrement les sites statiques et leurs domaines.",
		Permissions: []Permission{ProjectsGet, SitesList, SitesGet, SitesCreate, SitesUpdate, SitesDelete,
			SiteDeploymentsList, SiteDeploymentsCreate, SiteDeploymentsActive,
			SiteDomainsList, SiteDomainsCreate, SiteDomainsVerify, SiteDomainsDelete}},
	{Name: "roles/sites.deployer", Title: "Déployeur sites",
		Description: "Publie de nouvelles versions des sites (idéal pour la CI).",
		Permissions: []Permission{SitesList, SitesGet, SiteDeploymentsList, SiteDeploymentsCreate, SiteDeploymentsActive}},
	{Name: "roles/iam.serviceAccountUser", Title: "Utilisateur de compte de service",
		Description: "Peut exécuter des ressources en tant que le compte de service.",
		Permissions: []Permission{ServiceAccountsList, ServiceAccountsGet, ServiceAccountsActAs}},
	{Name: "roles/iam.serviceAccountAdmin", Title: "Administrateur comptes de service",
		Description: "Crée et gère les comptes de service et leurs clés.",
		Permissions: []Permission{ServiceAccountsList, ServiceAccountsGet, ServiceAccountsCreate, ServiceAccountsUpdate,
			ServiceAccountsDelete, ServiceAccountKeysList, ServiceAccountKeysCreate, ServiceAccountKeysDelete,
			APIKeysList, APIKeysCreate, APIKeysDelete}},
	{Name: "roles/iam.securityAdmin", Title: "Administrateur sécurité",
		Description: "Gère les accès de l'organisation et de ses projets, et lit le journal d'audit.",
		Permissions: []Permission{OrganizationsGet, OrganizationsGetIamPolicy, OrganizationsSetIamPolicy,
			ProjectsGet, ProjectsList, ProjectsGetIamPolicy, ProjectsSetIamPolicy,
			RolesList, RolesCreate, RolesUpdate, RolesDelete,
			ServiceAccountsList, ServiceAccountsGet, ServiceAccountKeysList, APIKeysList, APIKeysDelete,
			AuditLogsRead}},
	{Name: "roles/billing.admin", Title: "Administrateur facturation",
		Description: "Gère la facturation et les paiements.",
		Permissions: []Permission{OrganizationsGet, BillingAccountsGet, BillingAccountsUpdate, BillingPaymentsCreate}},
	{Name: "roles/logging.viewer", Title: "Lecteur journal d'audit",
		Description: "Lit le journal d'audit.",
		Permissions: []Permission{AuditLogsRead}},

	{Name: "roles/platform.admin", Title: "Administrateur plateforme",
		Description: "Staff betorun : opère la flotte de clusters.",
		Permissions: platformAdminPerms, PlatformOnly: true},
	{Name: "roles/platform.support", Title: "Support plateforme",
		Description: "Staff betorun : consulte la flotte.",
		Permissions: []Permission{PlatformClustersList, PlatformClusterKeysList}, PlatformOnly: true},
}

var rolesByName = func() map[string]Role {
	m := make(map[string]Role, len(predefinedRoles))
	for _, r := range predefinedRoles {
		m[r.Name] = r
	}
	return m
}()

// LookupRole : rôle prédéfini par nom.
func LookupRole(name string) (Role, bool) {
	r, ok := rolesByName[name]
	return r, ok
}

// PredefinedRoles : rôles attribuables par les clients (hors staff), triés.
func PredefinedRoles() []Role {
	out := make([]Role, 0, len(predefinedRoles))
	for _, r := range predefinedRoles {
		if !r.PlatformOnly {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// IsPredefinedRoleName : "roles/x" sans "/" supplémentaire. Les rôles
// personnalisés auront la forme "organizations/{o}/roles/{r}".
func IsPredefinedRoleName(name string) bool {
	return strings.HasPrefix(name, "roles/") && !strings.Contains(strings.TrimPrefix(name, "roles/"), "/")
}

func concat(a, b []Permission) []Permission {
	out := make([]Permission, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

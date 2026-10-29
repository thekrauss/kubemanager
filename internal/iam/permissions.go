// Package iam : autorisation de la plateforme (docs/architecture/iam.md).
//
// Une seule question, un seul endroit : Engine.Check(principal, permission,
// ressource). Les handlers ne raisonnent jamais sur des rôles.
//
// Ce package ne dépend d'aucun module métier : les modules dépendent de lui.
package iam

import "sort"

// Permission : "<service>.<ressource>.<verbe>". Une permission publiée n'est
// jamais renommée ni supprimée (les rôles personnalisés des clients la
// référencent) ; elle peut être dépréciée.
type Permission string

func (p Permission) String() string { return string(p) }

// Catalogue. Toute permission utilisée par une route doit figurer ici.
const (
	// resourcemanager
	OrganizationsGet          Permission = "resourcemanager.organizations.get"
	OrganizationsUpdate       Permission = "resourcemanager.organizations.update"
	OrganizationsGetIamPolicy Permission = "resourcemanager.organizations.getIamPolicy"
	OrganizationsSetIamPolicy Permission = "resourcemanager.organizations.setIamPolicy"
	ProjectsCreate            Permission = "resourcemanager.projects.create"
	ProjectsGet               Permission = "resourcemanager.projects.get"
	ProjectsList              Permission = "resourcemanager.projects.list"
	ProjectsUpdate            Permission = "resourcemanager.projects.update"
	ProjectsDelete            Permission = "resourcemanager.projects.delete"
	ProjectsGetIamPolicy      Permission = "resourcemanager.projects.getIamPolicy"
	ProjectsSetIamPolicy      Permission = "resourcemanager.projects.setIamPolicy"

	// workloads
	WorkloadsList     Permission = "workloads.workloads.list"
	WorkloadsGet      Permission = "workloads.workloads.get"
	WorkloadsCreate   Permission = "workloads.workloads.create"
	WorkloadsUpdate   Permission = "workloads.workloads.update"
	WorkloadsDelete   Permission = "workloads.workloads.delete"
	WorkloadsStart    Permission = "workloads.workloads.start"
	WorkloadsStop     Permission = "workloads.workloads.stop"
	WorkloadsRestart  Permission = "workloads.workloads.restart"
	WorkloadsRollback Permission = "workloads.workloads.rollback"
	WorkloadLogsRead  Permission = "workloads.logs.read"
	WorkloadMetrics   Permission = "workloads.metrics.read"

	// sites
	SitesList             Permission = "sites.sites.list"
	SitesGet              Permission = "sites.sites.get"
	SitesCreate           Permission = "sites.sites.create"
	SitesUpdate           Permission = "sites.sites.update"
	SitesDelete           Permission = "sites.sites.delete"
	SiteDeploymentsList   Permission = "sites.deployments.list"
	SiteDeploymentsCreate Permission = "sites.deployments.create"
	SiteDeploymentsActive Permission = "sites.deployments.activate"
	SiteDomainsList       Permission = "sites.domains.list"
	SiteDomainsCreate     Permission = "sites.domains.create"
	SiteDomainsVerify     Permission = "sites.domains.verify"
	SiteDomainsDelete     Permission = "sites.domains.delete"

	// iam
	ServiceAccountsList      Permission = "iam.serviceAccounts.list"
	ServiceAccountsGet       Permission = "iam.serviceAccounts.get"
	ServiceAccountsCreate    Permission = "iam.serviceAccounts.create"
	ServiceAccountsUpdate    Permission = "iam.serviceAccounts.update"
	ServiceAccountsDelete    Permission = "iam.serviceAccounts.delete"
	ServiceAccountsActAs     Permission = "iam.serviceAccounts.actAs"
	ServiceAccountKeysList   Permission = "iam.serviceAccountKeys.list"
	ServiceAccountKeysCreate Permission = "iam.serviceAccountKeys.create"
	ServiceAccountKeysDelete Permission = "iam.serviceAccountKeys.delete"
	APIKeysList              Permission = "iam.apiKeys.list"
	APIKeysCreate            Permission = "iam.apiKeys.create"
	APIKeysDelete            Permission = "iam.apiKeys.delete"
	RolesList                Permission = "iam.roles.list"
	RolesCreate              Permission = "iam.roles.create"
	RolesUpdate              Permission = "iam.roles.update"
	RolesDelete              Permission = "iam.roles.delete"
	GroupsList               Permission = "iam.groups.list"
	GroupsGet                Permission = "iam.groups.get"
	GroupsCreate             Permission = "iam.groups.create"
	GroupsUpdate             Permission = "iam.groups.update" // métadonnées et membres
	GroupsDelete             Permission = "iam.groups.delete"

	// billing / logging
	BillingAccountsGet    Permission = "billing.accounts.get"
	BillingAccountsUpdate Permission = "billing.accounts.update"
	BillingPaymentsCreate Permission = "billing.payments.create"
	AuditLogsRead         Permission = "logging.auditLogs.read"

	// platform : réservées au staff betorun, attribuables seulement sur "platform".
	PlatformClustersList      Permission = "platform.clusters.list"
	PlatformClusterKeysList   Permission = "platform.clusterKeys.list"
	PlatformClusterKeysCreate Permission = "platform.clusterKeys.create"
	PlatformClusterKeysRotate Permission = "platform.clusterKeys.rotate"
	PlatformClusterKeysRevoke Permission = "platform.clusterKeys.revoke"
	// PlatformClustersReport : le watcher d'un cluster rend compte de son
	// cluster (enrôlement, configuration, événements des pods).
	PlatformClustersReport Permission = "platform.clusters.report"
)

// PermissionInfo : métadonnées d'une permission (doc, UI, rôles personnalisés).
type PermissionInfo struct {
	Title string
	// CustomRoles : utilisable dans un rôle personnalisé client.
	CustomRoles bool
	// PlatformOnly : ne peut être accordée que sur la ressource "platform".
	PlatformOnly bool
	// Sensitive : action à fort impact (accès, clés, suppression, paiement) ;
	// une personne doit s'être authentifiée récemment avec double
	// authentification (step-up, iam.md §9).
	Sensitive bool
}

var catalog = map[Permission]PermissionInfo{
	OrganizationsGet:          {Title: "Voir une organisation", CustomRoles: true},
	OrganizationsUpdate:       {Title: "Modifier une organisation", CustomRoles: true, Sensitive: true},
	OrganizationsGetIamPolicy: {Title: "Voir les accès d'une organisation", CustomRoles: true},
	OrganizationsSetIamPolicy: {Title: "Modifier les accès d'une organisation", CustomRoles: true, Sensitive: true},
	ProjectsCreate:            {Title: "Créer un projet", CustomRoles: true},
	ProjectsGet:               {Title: "Voir un projet", CustomRoles: true},
	ProjectsList:              {Title: "Lister les projets", CustomRoles: true},
	ProjectsUpdate:            {Title: "Modifier un projet", CustomRoles: true},
	ProjectsDelete:            {Title: "Supprimer un projet", CustomRoles: true, Sensitive: true},
	ProjectsGetIamPolicy:      {Title: "Voir les accès d'un projet", CustomRoles: true},
	ProjectsSetIamPolicy:      {Title: "Modifier les accès d'un projet", CustomRoles: true, Sensitive: true},

	WorkloadsList:     {Title: "Lister les conteneurs", CustomRoles: true},
	WorkloadsGet:      {Title: "Voir un conteneur", CustomRoles: true},
	WorkloadsCreate:   {Title: "Déployer un conteneur", CustomRoles: true},
	WorkloadsUpdate:   {Title: "Modifier un conteneur", CustomRoles: true},
	WorkloadsDelete:   {Title: "Supprimer un conteneur", CustomRoles: true},
	WorkloadsStart:    {Title: "Démarrer un conteneur", CustomRoles: true},
	WorkloadsStop:     {Title: "Arrêter un conteneur", CustomRoles: true},
	WorkloadsRestart:  {Title: "Redémarrer un conteneur", CustomRoles: true},
	WorkloadsRollback: {Title: "Revenir à une version d'un conteneur", CustomRoles: true},
	WorkloadLogsRead:  {Title: "Lire les logs d'un conteneur", CustomRoles: true},
	WorkloadMetrics:   {Title: "Voir les métriques d'un conteneur", CustomRoles: true},

	SitesList:             {Title: "Lister les sites", CustomRoles: true},
	SitesGet:              {Title: "Voir un site", CustomRoles: true},
	SitesCreate:           {Title: "Créer un site", CustomRoles: true},
	SitesUpdate:           {Title: "Modifier un site", CustomRoles: true},
	SitesDelete:           {Title: "Supprimer un site", CustomRoles: true},
	SiteDeploymentsList:   {Title: "Voir les déploiements d'un site", CustomRoles: true},
	SiteDeploymentsCreate: {Title: "Déployer un site", CustomRoles: true},
	SiteDeploymentsActive: {Title: "Activer une version d'un site", CustomRoles: true},
	SiteDomainsList:       {Title: "Voir les domaines d'un site", CustomRoles: true},
	SiteDomainsCreate:     {Title: "Ajouter un domaine", CustomRoles: true},
	SiteDomainsVerify:     {Title: "Vérifier un domaine", CustomRoles: true},
	SiteDomainsDelete:     {Title: "Retirer un domaine", CustomRoles: true},

	ServiceAccountsList:      {Title: "Lister les comptes de service", CustomRoles: true},
	ServiceAccountsGet:       {Title: "Voir un compte de service", CustomRoles: true},
	ServiceAccountsCreate:    {Title: "Créer un compte de service", CustomRoles: true},
	ServiceAccountsUpdate:    {Title: "Modifier un compte de service", CustomRoles: true},
	ServiceAccountsDelete:    {Title: "Supprimer un compte de service", CustomRoles: true, Sensitive: true},
	ServiceAccountsActAs:     {Title: "Agir en tant que compte de service", CustomRoles: true},
	ServiceAccountKeysList:   {Title: "Lister les clés d'un compte de service", CustomRoles: true},
	ServiceAccountKeysCreate: {Title: "Créer une clé de compte de service", CustomRoles: true, Sensitive: true},
	ServiceAccountKeysDelete: {Title: "Supprimer une clé de compte de service", CustomRoles: true},
	APIKeysList:              {Title: "Lister les clés API", CustomRoles: true},
	APIKeysCreate:            {Title: "Créer une clé API", CustomRoles: true, Sensitive: true},
	APIKeysDelete:            {Title: "Révoquer une clé API", CustomRoles: true},
	RolesList:                {Title: "Lister les rôles", CustomRoles: true},
	RolesCreate:              {Title: "Créer un rôle personnalisé", CustomRoles: true, Sensitive: true},
	RolesUpdate:              {Title: "Modifier un rôle personnalisé", CustomRoles: true, Sensitive: true},
	RolesDelete:              {Title: "Supprimer un rôle personnalisé", CustomRoles: true, Sensitive: true},
	GroupsList:               {Title: "Lister les groupes", CustomRoles: true},
	GroupsGet:                {Title: "Voir un groupe et ses membres", CustomRoles: true},
	GroupsCreate:             {Title: "Créer un groupe", CustomRoles: true},
	GroupsUpdate:             {Title: "Modifier un groupe et ses membres", CustomRoles: true, Sensitive: true},
	GroupsDelete:             {Title: "Supprimer un groupe", CustomRoles: true, Sensitive: true},

	BillingAccountsGet:    {Title: "Voir la facturation", CustomRoles: true},
	BillingAccountsUpdate: {Title: "Modifier la facturation", CustomRoles: true, Sensitive: true},
	BillingPaymentsCreate: {Title: "Effectuer un paiement", CustomRoles: true, Sensitive: true},
	AuditLogsRead:         {Title: "Lire le journal d'audit", CustomRoles: true},

	PlatformClustersList:      {Title: "Lister les clusters de la plateforme", PlatformOnly: true},
	PlatformClusterKeysList:   {Title: "Lister les clés de cluster", PlatformOnly: true},
	PlatformClusterKeysCreate: {Title: "Créer une clé de cluster", PlatformOnly: true, Sensitive: true},
	PlatformClusterKeysRotate: {Title: "Faire tourner une clé de cluster", PlatformOnly: true, Sensitive: true},
	PlatformClusterKeysRevoke: {Title: "Révoquer une clé de cluster", PlatformOnly: true, Sensitive: true},
	PlatformClustersReport:    {Title: "Rendre compte de son cluster (watcher)", PlatformOnly: true},
}

// Known : la permission figure au catalogue.
func Known(p Permission) bool {
	_, ok := catalog[p]
	return ok
}

// Info : métadonnées d'une permission connue.
func Info(p Permission) (PermissionInfo, bool) {
	i, ok := catalog[p]
	return i, ok
}

// AllPermissions : catalogue trié (API /iam/v1/permissions, doc, UI).
func AllPermissions() []Permission {
	out := make([]Permission, 0, len(catalog))
	for p := range catalog {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

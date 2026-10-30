package iam

import "time"

// Rôles prédéfinis référencés par le code (le catalogue complet est dans
// roles.go).
const (
	// RolePrefix : préfixe des rôles prédéfinis ("roles/x").
	RolePrefix = "roles/"
	// CustomRoleSegment : segment des rôles personnalisés
	// ("organizations/{o}/roles/{r}").
	CustomRoleSegment = "/roles/"

	RoleOwner           = "roles/owner"
	RoleEditor          = "roles/editor"
	RoleDeveloper       = "roles/developer"
	RoleViewer          = "roles/viewer"
	RolePlatformAdmin   = "roles/platform.admin"
	RolePlatformSupport = "roles/platform.support"
)

// Collections racines des noms de ressources.
const (
	CollectionOrganizations = "organizations"
	CollectionProjects      = "projects"

	OrganizationsPrefix = CollectionOrganizations + "/"
	ProjectsPrefix      = CollectionProjects + "/"
)

// PrincipalSeparator : sépare le type et l'identifiant d'un principal
// ("user:<uuid>").
const PrincipalSeparator = ":"

// Acteurs système inscrits dans l'audit des modifications de policies
// effectuées par la plateforme elle-même.
const (
	ActorSystemRBAC          = "system:rbac"
	ActorSystemOrganizations = "system:organizations"
	ActorSystemConfig        = "system:config"
)

// day : unité des durées exprimées en jours (validité des clés, délais de grâce).
const day = 24 * time.Hour

// Longueurs maximales des champs libres.
const (
	maxDisplayNameLen = 100
	maxRoleTitleLen   = 100
	// maxIssuerLen : émetteur OIDC (fédération, clés de cluster).
	maxIssuerLen         = 255
	maxSubjectPatternLen = 255
)

// Statuts d'une clé de cluster (ClusterKey.Status).
const (
	ClusterKeyStatusActive   = "active"
	ClusterKeyStatusExpiring = "expiring"
	ClusterKeyStatusExpired  = "expired"
	ClusterKeyStatusRevoked  = "revoked"
)

// Noms par défaut des comptes et clés d'un agent de cluster (suivis de
// l'identifiant du cluster).
const (
	clusterAgentAccountNamePrefix = "Agent du cluster "
	clusterAgentKeyNamePrefix     = "watcher "
)

const (
	// saKeyIDBytes : octets aléatoires de l'identifiant d'une clé de compte de service.
	saKeyIDBytes = 16
	// etagBytes : octets aléatoires d'un etag de policy.
	etagBytes = 12
	// keyUsageUpdateTimeout : mise à jour asynchrone de last_used_at.
	keyUsageUpdateTimeout = 5 * time.Second
	// orgCacheTTLFactor : l'organisation d'un projet est mise en cache
	// orgCacheTTLFactor fois plus longtemps que les décisions.
	orgCacheTTLFactor = 60
)

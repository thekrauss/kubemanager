package api

import "time"

// Chemins de l'API, relatifs à l'URL de base (…/kmanager/v1).
const (
	pathIdentityRefresh         = "/identity/refresh"
	pathIdentityLogout          = "/identity/logout"
	pathIdentityDeviceCode      = "/identity/device/code"
	pathIdentityDeviceToken     = "/identity/device/token"
	pathIdentityOIDCAuthorize   = "/identity/oidc/authorize"
	pathIdentityOIDCCallback    = "/identity/oidc/callback"
	pathIdentityBootstrapStatus = "/identity/bootstrap/status"

	pathIAMSession     = "/iam/session"
	pathIAMPolicy      = "/iam/policy"
	pathIAMRoles       = "/iam/roles"
	pathIAMAncestry    = "/iam/ancestry"
	pathIAMPermissions = "/iam/permissions"
	pathIAMCustomRoles = "/iam/customRoles"
	pathIAMCustomRole  = "/iam/customRole"

	pathWorkloads     = "/workloads"
	pathProjects      = "/projects"
	pathOrganizations = "/organizations"

	// Sous-collections.
	segSites           = "/sites"
	segDeployments     = "/deployments"
	segDomains         = "/domains"
	segVerify          = "/verify"
	segLogs            = "/logs"
	segGroups          = "/groups"
	segMembers         = "/members"
	segServiceAccounts = "/serviceAccounts"
	segKeys            = "/keys"
	segFederation      = "/federation"
)

// Paramètres de requête.
const (
	queryResource  = "resource"
	queryProjectID = "project_id"
	queryParent    = "parent"
	queryName      = "name"
	queryMember    = "member"
)

// Client HTTP.
const (
	// userAgent : User-Agent des requêtes de la CLI.
	userAgent = "betorun-cli"
	// bearerPrefix : schéma de l'en-tête Authorization.
	bearerPrefix = "Bearer "
	// httpTimeout : délai maximal d'un appel (hors flux de logs).
	httpTimeout = 5 * time.Minute
	// sessionRefreshBefore : la session est renouvelée avant son expiration.
	sessionRefreshBefore = 30 * time.Second
	// maxResponseBytes / maxStreamErrorBytes : lecture bornée des réponses.
	maxResponseBytes    = 8 << 20
	maxStreamErrorBytes = 64 << 10
)

// errCodeStepUpRequired : l'action exige une double authentification récente.
const errCodeStepUpRequired = "STEP_UP_REQUIRED"

// Connexion par code d'appareil (RFC 8628).
const (
	deviceErrAuthorizationPending = "authorization_pending"
	deviceErrSlowDown             = "slow_down"
	deviceErrExpiredToken         = "expired_token"
	deviceErrAccessDenied         = "access_denied"
	// deviceSlowDownStep : intervalle ajouté à chaque slow_down.
	deviceSlowDownStep = 5 * time.Second
)

// Connexion par navigateur.
const (
	oidcClientCLI = "cli"
	// uploadFileName : nom de fichier de l'archive envoyée pour un site.
	uploadFileName = "site.zip"
)

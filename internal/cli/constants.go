package cli

import "time"

// Noms IAM (mêmes valeurs que internal/iam, non importé : la CLI reste légère).
const (
	prefixProjects      = "projects/"
	prefixOrganizations = "organizations/"
	prefixRoles         = "roles/"
	// segmentCustomRoles : rôles personnalisés (<parent>/roles/<id>).
	segmentCustomRoles = "/roles/"

	kindUser           = "user"
	kindGroup          = "group"
	kindServiceAccount = "serviceAccount"
	// kindServiceAccountShort : alias accepté en ligne de commande.
	kindServiceAccountShort = "sa"

	principalUser           = kindUser + ":"
	principalGroup          = kindGroup + ":"
	principalServiceAccount = kindServiceAccount + ":"
)

// Statuts et phases d'un workload (mêmes valeurs que internal/modules/utils).
const (
	statusRunning         = "RUNNING"
	statusFailed          = "FAILED"
	statusCrashLoop       = "CRASHLOOP"
	statusImagePullFailed = "IMAGE_PULL_FAILED"
	statusConfigError     = "CONFIG_ERROR"
	statusOOMKilled       = "OOM_KILLED"

	phaseDeployed          = "DEPLOYED"
	phaseStarted           = "STARTED"
	phaseHelmError         = "HELM_ERROR"
	phaseImageParseError   = "IMAGE_PARSE_ERROR"
	phaseSecretCreateError = "SECRET_CREATION_ERROR"
)

// Valeurs par défaut des options de « workloads deploy » et « logs ».
const (
	defaultTargetPort  = 8080
	defaultCPULimit    = "500m"
	defaultMemoryLimit = "512Mi"
	defaultLogTail     = 200
	// defaultSAKeyDays : validité d'une clé de compte de service créée par la CLI.
	defaultSAKeyDays = 90
)

// Connexion par navigateur : serveur local qui reçoit le code d'autorisation.
const (
	loopbackListenAddr        = "127.0.0.1:0"
	loopbackCallbackPath      = "/callback"
	loopbackRedirectFormat    = "http://127.0.0.1:%d" + loopbackCallbackPath
	loopbackReadHeaderTimeout = 5 * time.Second
)

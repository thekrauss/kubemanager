package activities

import "time"

// Exécution Helm (install / upgrade / uninstall des releases).
const (
	// helmStorageDriver : stockage de l'état des releases Helm (Secrets K8s).
	helmStorageDriver = "secret"

	helmInstallTimeout   = 5 * time.Minute
	helmUninstallTimeout = 3 * time.Minute

	// Fenêtres d'idempotence des activities (durée de la marque « en cours »).
	installChartProcessingTTL   = 10 * time.Minute
	uninstallChartProcessingTTL = 5 * time.Minute
	ensureSecretProcessingTTL   = 2 * time.Minute

	// Messages d'erreur Helm reconnus (texte renvoyé par le SDK).
	helmErrNoDeployedReleases = "has no deployed releases"
	helmErrReleaseNotFound    = "not found"
)

// Ressources par défaut d'un conteneur (si non précisées au déploiement).
const (
	defaultCPURequest    = "100m"
	defaultMemoryRequest = "128Mi"
	defaultCPULimit      = "500m"
	defaultMemoryLimit   = "512Mi"
)

// Valeurs par défaut du chart standard-app.
const (
	defaultServiceType     = "ClusterIP"
	defaultServicePort     = 80
	defaultServiceProtocol = "TCP"
	// containerPortName : nom du port du conteneur dans le chart (sondes).
	containerPortName = "http"

	defaultIngressClass     = "nginx"
	defaultIngressNamespace = "ingress-nginx"
	// defaultClusterIssuer : émetteur cert-manager des certificats TLS.
	defaultClusterIssuer = "letsencrypt-prod"
	// namespaceNameLabel : label posé par Kubernetes sur chaque espace de noms.
	namespaceNameLabel = "kubernetes.io/metadata.name"

	seccompProfileType = "RuntimeDefault"
	dropAllCapability  = "ALL"

	defaultAutoscalingMinReplicas = 1
	defaultAutoscalingMaxReplicas = 10
	defaultAutoscalingTargetCPU   = 75
	defaultPDBMinAvailable        = "50%"

	// defaultHealthPath : chemin des sondes si aucun n'est fourni.
	defaultHealthPath = "/"
)

// Sondes du conteneur (secondes).
const (
	startupProbePeriodSeconds    = 5
	startupProbeFailureThreshold = 30

	readinessProbeInitialDelaySeconds = 5
	readinessProbePeriodSeconds       = 10
	readinessProbeTimeoutSeconds      = 3
	readinessProbeFailureThreshold    = 3

	livenessProbeInitialDelaySeconds = 30
	livenessProbePeriodSeconds       = 20
	livenessProbeTimeoutSeconds      = 5
	livenessProbeFailureThreshold    = 3
)

// tmpfs montés quand le système de fichiers racine est en lecture seule.
const (
	tmpfsTmpName      = "tmp"
	tmpfsTmpPath      = "/tmp"
	tmpfsTmpSizeLimit = "64Mi"
	tmpfsRunName      = "run"
	tmpfsRunPath      = "/var/run"
	tmpfsRunSizeLimit = "16Mi"
)

// defaultImageTag : tag retenu quand l'image n'en précise pas (usage Docker).
const defaultImageTag = "latest"

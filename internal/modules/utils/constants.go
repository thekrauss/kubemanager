package utils

const (
	ProjectStatusPending      = "PENDING"
	ProjectStatusProvisioning = "PROVISIONING"
	ProjectStatusReady        = "READY"
	ProjectStatusError        = "ERROR"
	ProjectStatusDeleting     = "DELETING"
	ProjectStatusDeleted      = "DELETED"
	ProjectStatusSuspended    = "SUSPENDED"
)

const (
	PhaseDBInitializing      = "DB_INITIALIZING"
	PhaseK8sNamespaceLinking = "K8S_NS_CREATING"
	PhaseK8sQuotasApplying   = "K8S_QUOTAS_APPLYING"
	PhaseRBACSetting         = "K8S_RBAC_SETTING"
	PhaseProvisioningSuccess = "PROVISIONING_DONE"

	PhaseProvisioningFailed = "PROVISIONING_FAILED"
	PhaseRollbackInitiated  = "ROLLBACK_STARTED"
	PhaseRollbackCompleted  = "ROLLBACK_DONE"
)

// ProjectStatusUnknown : statut affiché quand celui enregistré n'est pas reconnu.
const ProjectStatusUnknown = "UNKNOWN_STATE"

// Statuts d'un workload (colonne workloads.status, exposés tels quels en JSON).
const (
	WorkloadStarting     = "STARTING"
	WorkloadRunning      = "RUNNING"
	WorkloadDegraded     = "DEGRADED"
	WorkloadFailed       = "FAILED"
	WorkloadScaling      = "SCALING"
	WorkloadStopped      = "STOPPED"
	WorkloadDeleting     = "DELETING"
	WorkloadDeleted      = "DELETED"
	WorkloadDeleteFailed = "DELETE_FAILED"

	// Statuts issus des événements de pods remontés par le watcher.
	WorkloadCrashLoop       = "CRASHLOOP"
	WorkloadImagePullFailed = "IMAGE_PULL_FAILED"
	WorkloadConfigError     = "CONFIG_ERROR"
	WorkloadOOMKilled       = "OOM_KILLED"
	WorkloadEvicted         = "EVICTED"
)

// Phases d'un workload (colonne workloads.current_phase).
const (
	PhaseHelmChartLoading      = "HELM_CHART_LOADING"
	PhaseHelmValuesInjecting   = "HELM_VALUES_INJECTING"
	PhaseHelmReleaseInstalling = "HELM_INSTALLING"
	PhaseHelmReleaseSuccess    = "HELM_SUCCESS"

	PhaseHelmPreparing     = "HELM_PREPARING"
	PhaseDeployed          = "DEPLOYED"
	PhaseHelmUninstalling  = "HELM_UNINSTALLING"
	PhaseRemoved           = "REMOVED"
	PhaseScaledToZero      = "SCALED_TO_ZERO"
	PhaseStarted           = "STARTED"
	PhaseRolloutRestart    = "ROLLOUT_RESTART"
	PhaseRolledBack        = "ROLLED_BACK"
	PhaseImageParseError   = "IMAGE_PARSE_ERROR"
	PhaseSecretCreateError = "SECRET_CREATION_ERROR"
	PhaseHelmError         = "HELM_ERROR"
	PhaseHelmUninstallErr  = "HELM_UNINSTALL_ERROR"
	PhaseDBDeleteError     = "DB_DELETE_ERROR"

	// PhasePodEventPrefix : préfixe des phases issues des événements de pods
	// remontés par le watcher (POD_<raison>).
	PhasePodEventPrefix = "POD_"
)

// Nommage Kubernetes des ressources d'un projet.
const (
	// NamespacePrefix : préfixe de l'espace de noms d'un projet (km-<projet>).
	NamespacePrefix = "km-"
	// EnvSecretSuffix : suffixe du Secret des variables d'un workload (<nom>-env).
	EnvSecretSuffix = "-env"
	// DefaultStorageClass : classe de stockage des volumes si non précisée.
	DefaultStorageClass = "local-path"
)

// Labels et annotations Kubernetes posés par la plateforme.
const (
	LabelManaged      = "kubemanager.io/managed"
	LabelProjectID    = "kubemanager.io/project-id"
	LabelProjectName  = "kubemanager.io/name"
	LabelCreatedAt    = "kubemanager.io/created-at"
	LabelManagedValue = "true"

	// Labels posés par le chart standard-app sur les pods et secrets.
	LabelKmProjectID  = "km-project-id"
	LabelKmWorkloadID = "km-workload-id"
	// LabelWatcherWorkloadID : label de repli lu dans les événements du watcher.
	LabelWatcherWorkloadID = "workload-id"
	// LabelApp : label du nom de l'application (release Helm).
	LabelApp = "app"

	// AnnotationRestartedAt : annotation de pod modifiée pour un rollout restart.
	AnnotationRestartedAt = "kubemanager.io/restartedAt"
)

// ProjectNamespace : espace de noms Kubernetes d'un projet.
func ProjectNamespace(projectName string) string {
	return NamespacePrefix + projectName
}

// EnvSecretName : Secret des variables d'environnement d'un workload.
func EnvSecretName(releaseName string) string {
	return releaseName + EnvSecretSuffix
}

const (
	HealthHealthy   = "HEALTHY"
	HealthUnhealthy = "UNHEALTHY"
	HealthUnknown   = "UNKNOWN"
)

func IsValidProjectStatus(status string) bool {
	switch status {
	case ProjectStatusPending, ProjectStatusProvisioning, ProjectStatusReady,
		ProjectStatusError, ProjectStatusDeleting, ProjectStatusSuspended:
		return true
	}
	return false
}

// Conversions d'unités de quantités Kubernetes.
const (
	BytesPerMi = 1024 * 1024
	BytesPerGi = 1024 * 1024 * 1024
)

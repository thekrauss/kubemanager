package utils

// --- Project Status (Machine State) ---
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

const (
	WorkloadStarting = "STARTING"
	WorkloadRunning  = "RUNNING"
	WorkloadDegraded = "DEGRADED"
	WorkloadFailed   = "FAILED"
	WorkloadScaling  = "SCALING"
)

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

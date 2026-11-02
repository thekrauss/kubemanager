package clusters

// Raisons Kubernetes des événements de pods transmises par le watcher.
const (
	reasonCrashLoopBackOff           = "CrashLoopBackOff"
	reasonImagePullBackOff           = "ImagePullBackOff"
	reasonCreateContainerConfigError = "CreateContainerConfigError"
	reasonOOMKilled                  = "OOMKilled"
	reasonFailed                     = "Failed"
	reasonEvicted                    = "Evicted"
)

// Résultat de l'enrôlement d'un cluster (EnrollResponse.Status).
const (
	enrollStatusEnrolled = "enrolled"
	enrollStatusUpdated  = "updated"
)

// Accusés de réception d'un événement de pod (PodEventAck.Status).
const (
	ackStatusAccepted = "accepted"
	ackStatusIgnored  = "ignored"
	ackStatusMapped   = "mapped"
)

// podEventType : type des messages temps réel d'événement de pod.
const podEventType = "pod_event"

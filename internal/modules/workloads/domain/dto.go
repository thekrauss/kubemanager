package domain

import "time"

type CreateWorkloadRequest struct {
	ProjectID string `json:"project_id" binding:"required" desc:"ID du projet parent"`
	Name      string `json:"name" binding:"required,min=3,max=30" desc:"Nom de la release Helm"`
	Image     string `json:"image" binding:"required" desc:"Image Docker (ex: nginx:latest)"`
	Replicas  int    `json:"replicas" default:"1"`

	// Resources
	CPURequest    string `json:"cpu_request" default:"100m"`
	MemoryRequest string `json:"memory_request" default:"128Mi"`
	CPULimit      string `json:"cpu_limit" default:"500m"`
	MemoryLimit   string `json:"memory_limit" default:"512Mi"`

	// Networking
	TargetPort  int    `json:"target_port" default:"8080"`
	ServiceType string `json:"service_type" default:"ClusterIP"` //"ClusterIP" ou "LoadBalancer"

	// Storage
	MountPath          string `json:"mount_path" default:"/data"`
	PersistenceEnabled bool   `json:"persistence_enabled" default:"false"`
	StorageSize        string `json:"storage_size" default:"1Gi"`
	StorageClass       string `json:"storage_class" default:"local-path"`

	// Secrets & env
	EnvVars    map[string]string `json:"env_vars"`
	SecretData map[string]string `json:"secret_data"`

	// Security — override du runAsUser/Group (ex: nginx=101, postgres=999).
	// 0 = utilise les defaults du chart (1000).
	RunAsUser  int64 `json:"run_as_user,omitempty"`
	RunAsGroup int64 `json:"run_as_group,omitempty"`

	// ReadOnlyRootFS : verrouille le filesystem root du container (opt-in).
	// Désactivé par défaut — beaucoup d'images courantes écrivent hors /tmp.
	ReadOnlyRootFS bool `json:"read_only_root_fs" default:"false"`

	// Autoscaling (opt-in, sinon replicas fixes).
	Autoscaling *AutoscalingConfig `json:"autoscaling,omitempty"`

	// Disruption budget (activé auto si replicas >= 2 et PDBEnabled).
	PDBEnabled bool `json:"pdb_enabled" default:"false"`

	// TLS ingress via cert-manager.
	TLSEnabled bool `json:"tls_enabled" default:"false"`

	// Probes paths (par défaut "/" sur targetPort).
	HealthPath    string `json:"health_path" default:"/"`
	ReadinessPath string `json:"readiness_path" default:"/"`

	// ServiceAccount : compte de service du projet sous lequel le workload
	// s'exécute ("serviceAccount:<uuid>" ou uuid). Ses pods obtiennent des
	// jetons betorun sans aucune clé. actAs exigé sur le compte.
	ServiceAccount string `json:"service_account,omitempty" desc:"Compte de service sous lequel le workload s'exécute (actAs requis)"`
}

// AutoscalingConfig : policy HPA simple (CPU + mémoire).
type AutoscalingConfig struct {
	Enabled                           bool  `json:"enabled"`
	MinReplicas                       int32 `json:"min_replicas" default:"1"`
	MaxReplicas                       int32 `json:"max_replicas" default:"10"`
	TargetCPUUtilizationPercentage    int32 `json:"target_cpu_percent" default:"75"`
	TargetMemoryUtilizationPercentage int32 `json:"target_memory_percent,omitempty"`
}

type WorkloadResponse struct {
	WorkloadID string `json:"workload_id"`
	Status     string `json:"status"`
	Namespace  string `json:"namespace"`
	Message    string `json:"message"`
}

type WorkloadStatusResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Phase       string    `json:"phase"`
	Image       string    `json:"image"`
	ExternalURL string    `json:"external_url,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UpdateWorkloadRequest struct {
	Image       string            `json:"image"`
	StorageSize string            `json:"storage_size" desc:"Nouvelle taille du disque (ex: 5Gi)"`
	EnvVars     map[string]string `json:"env_vars"`
}

// ── Observabilité ────────────────────────────────────────────────────────

// LiveStatus : snapshot merge DB + K8s runtime pour un workload donné.
type LiveStatus struct {
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	Namespace         string      `json:"namespace"`
	Status            string      `json:"status"` // DB-side : STARTING/RUNNING/FAILED...
	Phase             string      `json:"phase"`  // DB-side : HELM_PREPARING/DEPLOYED/...
	ExternalURL       string      `json:"external_url,omitempty"`
	Image             string      `json:"image"`
	Replicas          int32       `json:"replicas"`       // desired
	ReadyReplicas     int32       `json:"ready_replicas"` // K8s runtime
	AvailableReplicas int32       `json:"available_replicas"`
	Pods              []PodStatus `json:"pods,omitempty"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

// PodStatus : état d'un pod K8s (sous-ensemble pertinent pour l'UI).
type PodStatus struct {
	Name         string    `json:"name"`
	Phase        string    `json:"phase"` // Pending, Running, Succeeded, Failed, Unknown
	Ready        bool      `json:"ready"`
	RestartCount int32     `json:"restart_count"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	NodeName     string    `json:"node_name,omitempty"`
}

// WorkloadMetrics : consommation live (metrics.k8s.io) vs reserved (DB).
// Aggregate sur tous les pods portant le label `km-workload-id=<uuid>`.
type WorkloadMetrics struct {
	WorkloadID string `json:"workload_id"`
	// Actual — mesuré par le Metrics Server (moyenne ~1 min).
	CPUUsage    string `json:"cpu_usage_actual"`    // ex "120m"
	MemoryUsage string `json:"memory_usage_actual"` // ex "256Mi"
	PodCount    int    `json:"pod_count"`
	// Reserved — ce qui a été demandé côté Deploy (DB).
	ReservedCPU    string `json:"reserved_cpu"`    // ex "500m"
	ReservedMemory string `json:"reserved_memory"` // ex "512Mi"
	Replicas       int    `json:"replicas"`
}

// WorkloadEvent : K8s Event filtré sur un workload (via labelSelector).
type WorkloadEvent struct {
	Type      string    `json:"type"`   // Normal | Warning
	Reason    string    `json:"reason"` // Created, Pulled, FailedMount, ...
	Message   string    `json:"message"`
	Object    string    `json:"object"` // ex: "Pod/my-app-abc123"
	Count     int32     `json:"count"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

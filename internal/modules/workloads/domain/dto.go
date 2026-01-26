package domain

import "time"

type CreateWorkloadRequest struct {
	ProjectID string `json:"project_id" binding:"required" desc:"ID du projet parent"`
	Name      string `json:"name" binding:"required,min=3,max=30" desc:"Nom de la release Helm"`
	Image     string `json:"image" binding:"required" desc:"Image Docker (ex: nginx:latest)"`
	Replicas  int    `json:"replicas" default:"1"`

	CPULimit    string `json:"cpu_limit" default:"200m"`
	MemoryLimit string `json:"memory_limit" default:"256Mi"`

	TargetPort  int    `json:"target_port" default:"8080"`
	ServiceType string `json:"service_type" default:"ClusterIP"` //"ClusterIP" ou "LoadBalancer"
	MountPath   string `json:"mount_path" default:"/data"`

	PersistenceEnabled bool   `json:"persistence_enabled" default:"false"`
	StorageSize        string `json:"storage_size" default:"1Gi"`
	StorageClass       string `json:"storage_class" default:"local-path"`

	EnvVars    map[string]string `json:"env_vars"`
	SecretData map[string]string `json:"secret_data"`
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

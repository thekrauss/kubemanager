package domain

import (
	"time"

	"github.com/google/uuid"
)

type Workload struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:uuid_generate_v4()"`
	ProjectID uuid.UUID `gorm:"type:uuid;index;not null"`

	// Helm Identity
	Name      string `gorm:"not null"` // (frontend-prod")
	Namespace string `gorm:"not null"` // "km-alpha-project"
	ChartName string `gorm:"not null"` // "standard-app"
	Version   string `gorm:"not null"`

	Image string `gorm:"not null"` //  "nginx:latest"

	// Engine State
	Status       string `gorm:"not null;default:'STARTING'"`
	CurrentPhase string `gorm:"not null;default:'HELM_CHART_LOADING'"`

	Replicas int `gorm:"not null;default:1"`

	// Networking
	ExternalURL string `gorm:"type:text"` // ( https://app.vps-ip.sslip.io)

	Values string `gorm:"type:text"` // the final JSON sent to Helm

	CPULimit      string `gorm:"type:varchar(20);default:'200m'"`
	MemoryLimit   string `gorm:"type:varchar(20);default:'256Mi'"`
	CPURequest    string `gorm:"type:varchar(20);default:'100m'"`
	MemoryRequest string `gorm:"type:varchar(20);default:'128Mi'"`

	//   advence etworking
	TargetPort int    `gorm:"not null;default:8080"`
	Protocol   string `gorm:"type:varchar(10);default:'TCP'"`
	TLSEnabled bool   `gorm:"default:false"`

	//  health Checks
	LivenessPath string `gorm:"type:varchar(100);default:'/'"`

	//  metadata
	Labels         string `gorm:"type:text"` //JSON stringified pour les tags
	LastWorkflowID string `gorm:"type:varchar(100)"`

	// Storage (Persistence)
	PersistenceEnabled bool   `gorm:"default:false"`
	StorageSize        string `gorm:"type:varchar(20);default:'1Gi'"`
	StorageClass       string `gorm:"type:varchar(50);default:'local-path'"`
	AccessMode         string `gorm:"type:varchar(20);default:'ReadWriteOnce'"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

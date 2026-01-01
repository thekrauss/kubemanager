package domain

import (
	"time"

	"github.com/gofrs/uuid"
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

	// Networking
	ExternalURL string `gorm:"type:text"` // (ex https://app.vps-ip.sslip.io)

	Values string `gorm:"type:text"` // the final JSON sent to Helm

	CreatedAt time.Time
	UpdatedAt time.Time
}

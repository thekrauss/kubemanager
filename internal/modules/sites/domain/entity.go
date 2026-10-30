package domain

import (
	"time"

	"github.com/google/uuid"
)

// Statuts d'un site.
const (
	SiteStatusEmpty    = "EMPTY" // aucun déploiement actif
	SiteStatusLive     = "LIVE"
	SiteStatusDeleting = "DELETING"
)

// Statuts d'un déploiement.
const (
	DeploymentStatusPending    = "PENDING"
	DeploymentStatusProcessing = "PROCESSING"
	DeploymentStatusReady      = "READY"
	DeploymentStatusFailed     = "FAILED"
)

type Site struct {
	ID                 uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID          uuid.UUID  `gorm:"type:uuid;not null" json:"project_id"`
	Name               string     `gorm:"not null" json:"name"`
	Subdomain          string     `gorm:"not null" json:"subdomain"`
	SPAFallback        bool       `gorm:"column:spa_fallback;not null;default:false" json:"spa_fallback"`
	Status             string     `gorm:"not null;default:'EMPTY'" json:"status"`
	ActiveDeploymentID *uuid.UUID `gorm:"type:uuid" json:"active_deployment_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type SiteDeployment struct {
	ID         uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	SiteID     uuid.UUID  `gorm:"type:uuid;not null" json:"site_id"`
	Status     string     `gorm:"not null;default:'PENDING'" json:"status"`
	Error      string     `gorm:"not null;default:''" json:"error,omitempty"`
	ArchiveKey string     `gorm:"not null;default:''" json:"-"`
	FileCount  int        `gorm:"not null;default:0" json:"file_count"`
	SizeBytes  int64      `gorm:"not null;default:0" json:"size_bytes"`
	CreatedBy  *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type SiteDomain struct {
	ID         uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	SiteID     uuid.UUID  `gorm:"type:uuid;not null" json:"site_id"`
	Hostname   string     `gorm:"not null" json:"hostname"`
	Verified   bool       `gorm:"not null;default:false" json:"verified"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ObjectPrefix : préfixe des fichiers d'un déploiement dans l'object store.
// Partagé avec le binaire static-edge (internal/edge) — ne pas changer sans lui.
func ObjectPrefix(siteID, deploymentID uuid.UUID) string {
	return ObjectsRoot + siteID.String() + "/" + deploymentID.String() + "/"
}

// SitePrefix : tous les objets d'un site (déploiements + archives).
func SitePrefix(siteID uuid.UUID) string {
	return ObjectsRoot + siteID.String() + "/"
}

// ArchiveKey : emplacement de l'archive .zip uploadée, avant extraction.
func ArchiveKey(siteID, deploymentID uuid.UUID) string {
	return UploadsRoot + siteID.String() + "/" + deploymentID.String() + ArchiveExt
}

// UploadsPrefix : archives d'un site.
func UploadsPrefix(siteID uuid.UUID) string {
	return UploadsRoot + siteID.String() + "/"
}

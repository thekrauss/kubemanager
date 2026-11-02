package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Project struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name        string    `gorm:"type:varchar(255);uniqueIndex;not null"`
	Description string    `gorm:"type:text"`

	CpuLimit     string `gorm:"type:varchar(20);not null;default:'2000m'"`
	MemoryLimit  string `gorm:"type:varchar(20);not null;default:'4Gi'"`
	StorageLimit string `gorm:"type:varchar(20);not null;default:'10Gi'"`

	Status       string `gorm:"type:varchar(20);not null;default:'PENDING';index"`
	CurrentPhase string `gorm:"type:varchar(50);not null;default:'DB_INITIALIZING'"`

	// OrganizationID : organisation parente (hiérarchie IAM, cf. docs/architecture/iam.md).
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index"`

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

type User struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	KeycloakUserID string    `gorm:"type:varchar(255);uniqueIndex;not null;index"`
	LoginID        *string   `gorm:"type:varchar(50);uniqueIndex"`
	Email          string    `gorm:"type:varchar(255);uniqueIndex;not null;index"`
	FullName       string    `gorm:"type:varchar(255);not null"`
	AvatarURL      string    `gorm:"type:varchar(512)"`
	Country        string    `gorm:"type:char(2)"`
	PhoneNumber    *string   `gorm:"type:varchar(20)"`

	Role RoleType `gorm:"type:text;not null;default:'USER'"`

	Status      string `gorm:"type:varchar(20);not null;default:'active';index"`
	IsActive    bool   `gorm:"not null;default:true"`
	IsVerified  bool   `gorm:"not null;default:false"`
	LastLoginAt *time.Time

	Sessions    []UserSession   `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE;"`
	APIKeys     []APIKey        `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE;"`
	Memberships []ProjectMember `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE;"`

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

type UserSession struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID           uuid.UUID `gorm:"type:uuid;not null;index"`
	RefreshTokenHash string    `gorm:"type:varchar(128);not null;index"`
	UserAgent        string    `gorm:"type:varchar(512)"`
	ClientIP         string    `gorm:"type:varchar(45)"`
	ClientType       string    `gorm:"type:varchar(20);not null;default:'web'"`
	IsBlocked        bool      `gorm:"not null;default:false"`
	ExpiresAt        time.Time `gorm:"not null"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Role struct {
	ID   uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name string    `gorm:"type:varchar(50);uniqueIndex;not null"`

	Permissions []Permission `gorm:"many2many:role_permissions;"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

type Permission struct {
	ID   uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Slug string    `gorm:"type:varchar(100);uniqueIndex;not null"`
	Name string    `gorm:"type:varchar(120);not null"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

type RolePermission struct {
	RoleID       uuid.UUID `gorm:"type:uuid;primaryKey"`
	PermissionID uuid.UUID `gorm:"type:uuid;primaryKey"`
	CreatedAt    time.Time
}

type ProjectMember struct {
	// Vue « membre » calculée depuis les bindings IAM (la table project_members
	// n'existe plus, cf. repository/members_iam.go). Pas une entité GORM.
	ProjectID uuid.UUID
	Project   Project `gorm:"-"`

	UserID uuid.UUID
	User   User `gorm:"-"`

	Role Role `gorm:"-"`

	Status string

	JoinedAt  time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

type APIKey struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ProjectID uuid.UUID `gorm:"type:uuid;not null;index"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index"`
	// ClusterID : renseigné UNIQUEMENT pour Kind=cluster.
	// Permet lookup/rotation des cluster keys par identifiant cluster.
	ClusterID string `gorm:"type:varchar(100);index"`
	// ServiceAccountID : compte de service auquel la clé est rattachée
	// (migration 000007) ; la clé s'authentifie alors comme ce compte.
	ServiceAccountID *uuid.UUID     `gorm:"type:uuid"`
	Kind             string         `gorm:"type:varchar(20);not null;default:'backend';index"`
	Name             string         `gorm:"type:varchar(255)"`
	Prefix           string         `gorm:"type:varchar(100);not null;index"`
	KeyHash          string         `gorm:"type:varchar(128);not null"`
	Scopes           pq.StringArray `gorm:"type:text[]"`
	ExpiresAt        *time.Time
	RevokedAt        *time.Time
	IsActive         bool `gorm:"not null;default:true"`
	UsageCount       int  `gorm:"not null;default:0"`
	CreatedAt        time.Time
	LastUsedAt       *time.Time
}

type RevokedToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	TokenHash string    `gorm:"type:varchar(64);not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}

const (
	ProjectStatusPending   = "PENDING"
	ProjectStatusActive    = "ACTIVE"
	ProjectStatusSuspended = "SUSPENDED"
	ProjectStatusDeleted   = "DELETED"
)

const (
	ProjectPhaseDBInitializing = "DB_INITIALIZING"
	ProjectPhaseProvisioning   = "PROVISIONING"
	ProjectPhaseDeploying      = "DEPLOYING"
	ProjectPhaseReady          = "READY"
	ProjectPhaseFailed         = "FAILED"
)

const (
	ProjectMemberStatusPending  = "PENDING"
	ProjectMemberStatusApproved = "APPROVED"
	ProjectMemberStatusActive   = "ACTIVE"
	ProjectMemberStatusRejected = "REJECTED"
)

const (
	GlobalRoleSuperAdmin = "SUPER_ADMIN"
	GlobalRoleAdmin      = "ADMIN"
	GlobalRoleUser       = "USER"
	GlobalRoleService    = "SERVICE"
)

const (
	ProjectRoleOwner  = "OWNER"
	ProjectRoleAdmin  = "ADMIN"
	ProjectRoleMember = "MEMBER"
	ProjectRoleViewer = "VIEWER"
)

const (
	APIKeyKindBackend    = "backend"
	APIKeyKindAgent      = "agent"
	APIKeyKindCLI        = "cli"
	APIKeyKindAutomation = "automation"
	// APIKeyKindCluster : API key attribuée à un cluster K8s (watcher).
	// Utilisée uniquement par le binaire `watcher` déployé dans le cluster
	// pour notifier les pod events → kubemanager.
	APIKeyKindCluster = "cluster"
)

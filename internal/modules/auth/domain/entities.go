package domain

import (
	"time"

	"github.com/google/uuid"
)

type Project struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;default:uuid_generate_v4()"`
	Name        string    `gorm:"unique;not null"`
	Description string

	CpuLimit     string `gorm:"type:varchar(20);default:'2000m'"` // 2 vCPUs
	MemoryLimit  string `gorm:"type:varchar(20);default:'4Gi'"`   // 4 Go RAM
	StorageLimit string `gorm:"type:varchar(20);default:'10Gi'"`  // 10 Go Disque

	Members      []ProjectMember `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE;"`
	Status       string          `gorm:"default:'PENDING'"`
	CurrentPhase string          `gorm:"default:'DB_INITIALIZING'"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

type User struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;default:uuid_generate_v4()"`
	LoginID      *string   `gorm:"type:varchar(50)" json:"login_id"`
	Email        string    `gorm:"uniqueIndex;not null"`
	PasswordHash string    `gorm:"not null"`
	FullName     string
	AvatarURL    string

	Role string `gorm:"default:'USER'"`

	IsActive   bool `gorm:"default:true"`
	IsVerified bool `gorm:"default:false"`

	Sessions    []UserSession   `gorm:"foreignKey:UserID"`
	APIKeys     []APIKey        `gorm:"foreignKey:UserID"`
	Memberships []ProjectMember `gorm:"foreignKey:UserID"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

type UserSession struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;default:uuid_generate_v4()"`
	UserID           uuid.UUID `gorm:"not null"`
	RefreshTokenHash string    `gorm:"not null"`
	UserAgent        string
	ClientIP         string
	IsBlocked        bool      `gorm:"default:false"`
	ExpiresAt        time.Time `gorm:"not null"`
	CreatedAt        time.Time
}

type Role struct {
	ID          uuid.UUID    `gorm:"type:uuid;primary_key"`
	Name        string       `gorm:"unique"`
	Permissions []Permission `gorm:"many2many:role_permissions;"`
}

type Permission struct {
	ID   uuid.UUID `gorm:"type:uuid;primary_key"`
	Slug string    `gorm:"type:varchar(100);unique;not null"`
}

type ProjectMember struct {
	ProjectID uuid.UUID `gorm:"primaryKey"`
	Project   Project   `gorm:"foreignKey:ProjectID"`

	UserID uuid.UUID `gorm:"primaryKey"`
	User   User      `gorm:"foreignKey:UserID"`

	RoleID uuid.UUID
	Role   Role `gorm:"foreignKey:RoleID"`

	JoinedAt time.Time
}

type APIKey struct {
	ID     uuid.UUID `gorm:"type:uuid;primary_key;default:uuid_generate_v4()"`
	UserID uuid.UUID `gorm:"not null"`

	Name    string `gorm:"type:varchar(100);not null"`
	Prefix  string `gorm:"type:varchar(10);not null;index"`
	KeyHash string `gorm:"type:varchar(255);not null"`

	Scopes []PermissionType `gorm:"type:jsonb;serializer:json"`

	LastUsedAt *time.Time
	ExpiresAt  *time.Time

	CreatedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

type AuthRepository interface {
	CreateUser(ctx context.Context, user *domain.User) error
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	UpdateUser(ctx context.Context, user *domain.User) error

	ListProjectMembers(ctx context.Context, projectID uuid.UUID) ([]domain.ProjectMember, error)

	CreateSession(ctx context.Context, session *domain.UserSession) error
	GetSessionByID(ctx context.Context, id uuid.UUID) (*domain.UserSession, error)
	RevokeSession(ctx context.Context, id uuid.UUID) error
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error

	CheckProjectPermission(ctx context.Context, userID uuid.UUID, projectID uuid.UUID, permissionSlug string) (bool, error)
	IsTokenRevoked(ctx context.Context, tokenHash string) (bool, error)
	RevokeToken(ctx context.Context, tokenHash string, expiresAt time.Time) error

	CreateAPIKey(ctx context.Context, key *domain.APIKey) error
	GetAPIKeyByPrefix(ctx context.Context, prefix string) (*domain.APIKey, error)
	GetAPIKeyByID(ctx context.Context, keyID uuid.UUID) (*domain.APIKey, error)
	ListUserAPIKeys(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error)
	// ListAPIKeysByKind : toutes les clés d'un Kind donné (ex: "cluster").
	// Ordre : created_at DESC. Inclut les clés révoquées (filtre côté service).
	ListAPIKeysByKind(ctx context.Context, kind string) ([]domain.APIKey, error)
	// UpdateAPIKeyExpiration : set ExpiresAt (utilisé pour rotation avec grace period).
	UpdateAPIKeyExpiration(ctx context.Context, keyID uuid.UUID, expiresAt *time.Time) error
	RevokeAPIKey(ctx context.Context, keyID uuid.UUID, userID uuid.UUID) error
	UpdateAPIKeyUsage(ctx context.Context, keyID uuid.UUID) error
	// BatchIncrementAPIKeyUsage : update groupé de usage_count + last_used_at
	// via une seule query UPDATE ... FROM (VALUES ...) — évite le hot row
	// lock contention sur une clé très utilisée. (fix P4)
	BatchIncrementAPIKeyUsage(ctx context.Context, increments map[uuid.UUID]int) error

	AddProjectMember(ctx context.Context, member *domain.ProjectMember) error
	RemoveProjectMember(ctx context.Context, projectID uuid.UUID, userID uuid.UUID) error
	GetProjectMember(ctx context.Context, projectID uuid.UUID, userID uuid.UUID) (*domain.ProjectMember, error)
	GetUserProjectMemberships(ctx context.Context, userID uuid.UUID) ([]domain.ProjectMember, error)

	FindRefreshTokenByJTI(ctx context.Context, jti uuid.UUID) (*domain.UserSession, error)
	DeleteRefreshTokenByJTI(ctx context.Context, jti uuid.UUID) error

	Upsert(ctx context.Context, member *domain.ProjectMember) error
	FindFirstActiveByUser(ctx context.Context, userID uuid.UUID) (*domain.ProjectMember, error)
	FindByKeycloakUserID(ctx context.Context, kcUserID string) (*domain.User, error)

}

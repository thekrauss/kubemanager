package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

type AuthRepository interface {
	CreateUser(ctx context.Context, user *domain.User) error
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	UpdateUser(ctx context.Context, user *domain.User) error
	UpdatePassword(ctx context.Context, userID uuid.UUID, hash string) error

	SeedDefaultRoles(ctx context.Context) error
	ListRoles(ctx context.Context) ([]domain.Role, error)
	ListProjectMembers(ctx context.Context, projectID uuid.UUID) ([]domain.ProjectMember, error)

	CreateSession(ctx context.Context, session *domain.UserSession) error
	GetSessionByID(ctx context.Context, id uuid.UUID) (*domain.UserSession, error)
	RevokeSession(ctx context.Context, id uuid.UUID) error
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error

	CheckProjectPermission(ctx context.Context, userID uuid.UUID, projectID uuid.UUID, permissionSlug string) (bool, error)

	CreateAPIKey(ctx context.Context, key *domain.APIKey) error
	GetAPIKeyByPrefix(ctx context.Context, prefix string) (*domain.APIKey, error)
	ListUserAPIKeys(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, keyID uuid.UUID, userID uuid.UUID) error
	UpdateAPIKeyUsage(ctx context.Context, keyID uuid.UUID) error

	GetRoleByName(ctx context.Context, name string) (*domain.Role, error)
	AddProjectMember(ctx context.Context, member *domain.ProjectMember) error
	RemoveProjectMember(ctx context.Context, projectID uuid.UUID, userID uuid.UUID) error
	GetProjectMember(ctx context.Context, projectID uuid.UUID, userID uuid.UUID) (*domain.ProjectMember, error)
	GetUserProjectMemberships(ctx context.Context, userID uuid.UUID) ([]domain.ProjectMember, error)

	FindRefreshTokenByJTI(ctx context.Context, jti uuid.UUID) (*domain.UserSession, error)
	DeleteRefreshTokenByJTI(ctx context.Context, jti uuid.UUID) error
}

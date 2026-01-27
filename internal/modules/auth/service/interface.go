package service

import (
	"context"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

type IAuthService interface {
	Login(ctx context.Context, req *domain.LoginRequest) (*domain.LoginResponse, error)
	Logout(ctx context.Context, req *domain.LogoutRequest) error
	ValidateToken(ctx context.Context, req *domain.TokenRequest) (*domain.TokenResponse, error)
	RefreshToken(ctx context.Context, req *domain.RefreshTokenRequest) (*domain.RefreshTokenResponse, error)
}

type IRBACService interface {
	ListAllRoles(ctx context.Context) ([]domain.RoleDTO, error)
	ListProjectMembers(ctx context.Context, projectIDStr string) ([]domain.ProjectMemberDTO, error)
	RevokeProjectAccess(ctx context.Context, projectIDStr, userIDStr string) error
	AssignProjectRole(ctx context.Context, req *domain.AssignRoleRequest) error
	CheckPermission(ctx context.Context, req *domain.CheckPermissionRequest) (bool, error)
	GetUserProjectPermissions(ctx context.Context, projectIDStr, userIDStr string) (*domain.PermissionsResponse, error)
}

type IAPIKeyService interface {
	CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (*APIKeyCreatedResponse, error)
	ListUserKeys(ctx context.Context, userIDStr string) ([]APIKeyDTO, error)
	RevokeKey(ctx context.Context, userIDStr, keyIDStr string) error
}

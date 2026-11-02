package service

import (
	"context"

	"github.com/thekrauss/kubemanager/internal/iam"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

type IRBACService interface {
	ListAllRoles(ctx context.Context) ([]domain.RoleDTO, error)
	ListProjectMembers(ctx context.Context, projectIDStr string) ([]domain.ProjectMemberDTO, error)
	RevokeProjectAccess(ctx context.Context, caller iam.Principal, projectIDStr, userIDStr string) error
	AssignProjectRole(ctx context.Context, caller iam.Principal, req *domain.AssignRoleRequest) error
	CheckPermission(ctx context.Context, req *domain.CheckPermissionRequest) (bool, error)
	GetUserProjectPermissions(ctx context.Context, projectIDStr, userIDStr string) (*domain.PermissionsResponse, error)
}

type IAPIKeyService interface {
	CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (*APIKeyCreatedResponse, error)
	ListUserKeys(ctx context.Context, userIDStr string) ([]APIKeyDTO, error)
	GetKey(ctx context.Context, userIDStr, keyIDStr string) (*APIKeyDTO, error)
	ListAudit(ctx context.Context, userIDStr string) ([]APIKeyAuditEntry, error)
	RevokeKey(ctx context.Context, userIDStr, keyIDStr string) error
	ValidateKey(ctx context.Context, rawKey string) (*APIKeyValidation, error)
}

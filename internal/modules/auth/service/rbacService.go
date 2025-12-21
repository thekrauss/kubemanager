package service

import (
	"context"

	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"go.uber.org/zap"
)

type RBACService struct {
	Repo   repository.AuthRepository
	Logger *zap.SugaredLogger
}

func NewRBACService(repo repository.AuthRepository, logger *zap.SugaredLogger) *RBACService {
	return &RBACService{
		Repo:   repo,
		Logger: logger,
	}
}

func (s *RBACService) AssignProjectRole(ctx context.Context, req *domain.AssignRoleRequest) error {
	uID, err := uuid.Parse(req.UserID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid user id")
	}
	pID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid project id")
	}

	role, err := s.Repo.GetRoleByName(ctx, req.RoleName)
	if err != nil {
		return betoerrors.New(betoerrors.CodeNotFound, "role not found")
	}

	member := &domain.ProjectMember{
		ProjectID: pID,
		UserID:    uID,
		RoleID:    role.ID,
	}

	if err := s.Repo.AddProjectMember(ctx, member); err != nil {
		s.Logger.Errorw("failed to assign role", "error", err)
		return betoerrors.New(betoerrors.CodeInternal, "failed to assign role")
	}

	s.Logger.Infow("Role assigned successfully", "user", req.UserID, "project", req.ProjectID, "role", req.RoleName)
	return nil
}

func (s *RBACService) CheckPermission(ctx context.Context, req *domain.CheckPermissionRequest) (bool, error) {
	uID, _ := uuid.Parse(req.UserID)
	pID, _ := uuid.Parse(req.ProjectID)

	hasPerm, err := s.Repo.CheckProjectPermission(ctx, uID, pID, req.Permission)
	if err != nil {
		return false, err
	}
	return hasPerm, nil
}

func (s *RBACService) GetUserProjectPermissions(ctx context.Context, projectIDStr, userIDStr string) (*domain.PermissionsResponse, error) {
	uID, _ := uuid.Parse(userIDStr)
	pID, _ := uuid.Parse(projectIDStr)

	member, err := s.Repo.GetProjectMember(ctx, pID, uID)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeNotFound, "user is not a member of this project")
	}

	permSlugs := make([]string, len(member.Role.Permissions))
	for i, p := range member.Role.Permissions {
		permSlugs[i] = p.Slug
	}

	return &domain.PermissionsResponse{
		Role:        member.Role.Name,
		Permissions: permSlugs,
	}, nil
}

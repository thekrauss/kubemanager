package service

import (
	"context"

	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
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

func (s *RBACService) ListAllRoles(ctx context.Context) ([]domain.RoleDTO, error) {
	roles, err := s.Repo.ListRoles(ctx)
	if err != nil {
		return nil, betoerrors.Wrap(err, betoerrors.CodeInternal, "failed to list roles")
	}

	var result []domain.RoleDTO
	for _, r := range roles {
		perms := make([]string, len(r.Permissions))
		for i, p := range r.Permissions {
			perms[i] = p.Slug.String()
		}
		result = append(result, domain.RoleDTO{
			ID:          r.ID.String(),
			Name:        r.Name,
			Permissions: perms,
		})
	}
	return result, nil
}

func (s *RBACService) ListProjectMembers(ctx context.Context, projectIDStr string) ([]domain.ProjectMemberDTO, error) {
	pID, err := uuid.Parse(projectIDStr)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid project id")
	}

	members, err := s.Repo.ListProjectMembers(ctx, pID)
	if err != nil {
		return nil, betoerrors.Wrap(err, betoerrors.CodeInternal, "failed to fetch members")
	}

	var result []domain.ProjectMemberDTO
	for _, m := range members {
		result = append(result, domain.ProjectMemberDTO{
			UserID:    m.UserID.String(),
			Email:     m.User.Email,
			FullName:  m.User.FullName,
			AvatarURL: m.User.AvatarURL,
			RoleName:  m.Role.Name,
			JoinedAt:  m.JoinedAt.Format("2006-01-02"),
		})
	}

	return result, nil
}

func (s *RBACService) RevokeProjectAccess(ctx context.Context, projectIDStr, userIDStr string) error {
	pID, err := uuid.Parse(projectIDStr)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid project id")
	}
	uID, err := uuid.Parse(userIDStr)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid user id")
	}

	_, err = s.Repo.GetProjectMember(ctx, pID, uID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeNotFound, "member not found in this project")
	}

	if err := s.Repo.RemoveProjectMember(ctx, pID, uID); err != nil {
		s.Logger.Errorw("failed to revoke access", "project", pID, "user", uID, "error", err)
		return betoerrors.Wrap(err, betoerrors.CodeInternal, "failed to remove member")
	}

	s.Logger.Infow("Project access revoked", "project", pID, "user", uID)
	return nil
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
		permSlugs[i] = p.Slug.String()
	}

	return &domain.PermissionsResponse{
		Role:        member.Role.Name,
		Permissions: permSlugs,
	}, nil
}

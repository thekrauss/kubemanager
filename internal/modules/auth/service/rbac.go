package service

import (
	"context"
	"strings"

	"github.com/thekrauss/kubemanager/internal/iam"

	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
)

type RBACService struct {
	// Policies : écritures des membres via setIamPolicy (contrôles IAM).
	Policies *iam.PolicyService
	Repo     repository.AuthRepository
	Logger   *zap.SugaredLogger
}

func NewRBACService(repo repository.AuthRepository, logger *zap.SugaredLogger) *RBACService {
	return &RBACService{
		Repo:   repo,
		Logger: logger,
	}
}

// ListAllRoles : rôles projet legacy (OWNER, DEVELOPER, VIEWER), définis dans
// le code. Les rôles IAM complets sont sur GET /iam/roles.
func (s *RBACService) ListAllRoles(ctx context.Context) ([]domain.RoleDTO, error) {
	var result []domain.RoleDTO
	for _, role := range []domain.RoleType{domain.RoleTypes.Owner, domain.RoleTypes.Developer, domain.RoleTypes.Viewer} {
		perms := domain.RolePermissions(role)
		slugs := make([]string, len(perms))
		for i, p := range perms {
			slugs[i] = p.String()
		}
		result = append(result, domain.RoleDTO{ID: role.String(), Name: role.String(), Permissions: slugs})
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

// RevokeProjectAccess retire tous les rôles de l'utilisateur sur le projet.
// Passe par setIamPolicy : le dernier owner humain est protégé.
func (s *RBACService) RevokeProjectAccess(ctx context.Context, caller iam.Principal, projectIDStr, userIDStr string) error {
	pID, err := uuid.Parse(projectIDStr)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid project id")
	}
	uID, err := uuid.Parse(userIDStr)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid user id")
	}
	if _, err := s.Repo.GetProjectMember(ctx, pID, uID); err != nil {
		return betoerrors.New(betoerrors.CodeNotFound, "member not found in this project")
	}
	if err := s.setMemberRole(ctx, caller, pID, uID, ""); err != nil {
		return err
	}
	s.Logger.Infow("Project access revoked", "project", pID, "user", uID, "by", caller)
	return nil
}

// AssignProjectRole donne à l'utilisateur un rôle sur le projet, à la place
// de ses rôles précédents. Rôle legacy (OWNER, DEVELOPER, VIEWER) ou rôle IAM
// prédéfini ("roles/…"). Passe par setIamPolicy : mêmes contrôles qu'à
// travers l'API IAM (anti-escalade, dernier owner, membre existant).
func (s *RBACService) AssignProjectRole(ctx context.Context, caller iam.Principal, req *domain.AssignRoleRequest) error {
	uID, err := uuid.Parse(req.UserID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid user id")
	}
	pID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid project id")
	}
	iamRole := domain.LegacyRoleToIAM(strings.TrimSpace(req.RoleName))
	if role, ok := iam.LookupRole(iamRole); !ok || role.PlatformOnly {
		return betoerrors.New(betoerrors.CodeNotFound, "role not found")
	}
	if err := s.setMemberRole(ctx, caller, pID, uID, iamRole); err != nil {
		return err
	}
	s.Logger.Infow("Role assigned", "user", uID, "project", pID, "role", iamRole, "by", caller)
	return nil
}

// setMemberRole : read-modify-write de la policy du projet. role vide = retrait.
func (s *RBACService) setMemberRole(ctx context.Context, caller iam.Principal, projectID, userID uuid.UUID, role string) error {
	if s.Policies == nil {
		return betoerrors.New(betoerrors.CodeInternal, "iam policies not configured")
	}
	project, member := iam.ProjectName(projectID), iam.UserPrincipal(userID)
	view, err := s.Policies.Get(ctx, project)
	if err != nil {
		return err
	}
	policy := view.Policy
	var bindings []iam.PolicyBinding
	placed := false
	for _, b := range policy.Bindings {
		var members []iam.Principal
		for _, m := range b.Members {
			if m != member {
				members = append(members, m)
			}
		}
		if role != "" && !placed && b.Role == role && b.Condition == nil {
			members = append(members, member)
			placed = true
		}
		if len(members) > 0 {
			b.Members = members
			bindings = append(bindings, b)
		}
	}
	if role != "" && !placed {
		bindings = append(bindings, iam.PolicyBinding{Role: role, Members: []iam.Principal{member}})
	}
	policy.Bindings = bindings
	_, err = s.Policies.Set(ctx, caller, project, policy)
	return err
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

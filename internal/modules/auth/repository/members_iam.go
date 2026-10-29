package repository

// Membres de projet, adossés à l'IAM (docs/architecture/iam.md, étape A8).
//
// La table project_members n'existe plus : un « membre » est un utilisateur
// ayant un binding sans condition sur projects/{id}. Les signatures restent
// celles de l'ancien RBAC pour que les appelants (identity, RBAC) ne changent
// pas ; les noms de rôles legacy (OWNER, DEVELOPER, VIEWER) sont convertis
// dans les deux sens.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

// iamToLegacy : affichage legacy ; les autres rôles IAM gardent leur nom.
var iamToLegacy = map[string]string{
	"roles/owner":     domain.RoleTypes.Owner.String(),
	"roles/developer": domain.RoleTypes.Developer.String(),
	"roles/viewer":    domain.RoleTypes.Viewer.String(),
}

// roleRank : quand un utilisateur a plusieurs rôles sur un projet, le plus
// fort représente son appartenance.
var roleRank = map[string]int{"roles/owner": 4, "roles/editor": 3, "roles/developer": 2, "roles/viewer": 1}

type memberRow struct {
	ProjectID uuid.UUID
	UserID    uuid.UUID
	Role      string
	CreatedAt time.Time
}

// projectMembers : bindings utilisateur sans condition, au niveau projet.
func (r *pgAuthRepo) projectMembers(ctx context.Context, where string, args ...any) ([]domain.ProjectMember, error) {
	var rows []memberRow
	q := `SELECT substring(resource_name from 10)::uuid AS project_id,
	             substring(principal from 6)::uuid AS user_id, role, created_at
	      FROM iam_bindings
	      WHERE resource_name LIKE 'projects/%' AND resource_name NOT LIKE 'projects/%/%'
	        AND principal LIKE 'user:%' AND condition IS NULL`
	if where != "" {
		q += " AND " + where
	}
	if err := r.db.WithContext(ctx).Raw(q+" ORDER BY created_at, resource_name", args...).Scan(&rows).Error; err != nil {
		return nil, err
	}

	type key struct{ p, u uuid.UUID }
	best := map[key]memberRow{}
	var order []key
	for _, row := range rows {
		k := key{row.ProjectID, row.UserID}
		cur, ok := best[k]
		if !ok {
			order = append(order, k)
			best[k] = row
			continue
		}
		if roleRank[row.Role] > roleRank[cur.Role] {
			row.CreatedAt = cur.CreatedAt // date d'arrivée = premier binding
			best[k] = row
		}
	}

	out := make([]domain.ProjectMember, 0, len(order))
	for _, k := range order {
		row := best[k]
		name, ok := iamToLegacy[row.Role]
		if !ok {
			name = row.Role
		}
		out = append(out, domain.ProjectMember{
			ProjectID: row.ProjectID, UserID: row.UserID, Status: domain.ProjectMemberStatusActive,
			Role: domain.Role{Name: name}, JoinedAt: row.CreatedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

func (r *pgAuthRepo) withUsers(ctx context.Context, members []domain.ProjectMember) error {
	if len(members) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(members))
	for i, m := range members {
		ids[i] = m.UserID
	}
	var users []domain.User
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error; err != nil {
		return err
	}
	byID := make(map[uuid.UUID]domain.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	for i := range members {
		members[i].User = byID[members[i].UserID]
	}
	return nil
}

func (r *pgAuthRepo) GetProjectMember(ctx context.Context, projectID, userID uuid.UUID) (*domain.ProjectMember, error) {
	ms, err := r.projectMembers(ctx, "resource_name = ? AND principal = ?",
		iam.ProjectName(projectID).String(), iam.UserPrincipal(userID).String())
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if err := r.withUsers(ctx, ms); err != nil {
		return nil, err
	}
	return &ms[0], nil
}

func (r *pgAuthRepo) ListProjectMembers(ctx context.Context, projectID uuid.UUID) ([]domain.ProjectMember, error) {
	ms, err := r.projectMembers(ctx, "resource_name = ?", iam.ProjectName(projectID).String())
	if err != nil {
		return nil, err
	}
	if err := r.withUsers(ctx, ms); err != nil {
		return nil, err
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].JoinedAt.Before(ms[j].JoinedAt) })
	return ms, nil
}

func (r *pgAuthRepo) GetUserProjectMemberships(ctx context.Context, userID uuid.UUID) ([]domain.ProjectMember, error) {
	return r.projectMembers(ctx, "principal = ?", iam.UserPrincipal(userID).String())
}

// FindFirstActiveByUser : plus ancien projet auquel l'utilisateur a accès
// (gorm.ErrRecordNotFound s'il n'en a aucun).
func (r *pgAuthRepo) FindFirstActiveByUser(ctx context.Context, userID uuid.UUID) (*domain.ProjectMember, error) {
	ms, err := r.GetUserProjectMemberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &ms[0], nil
}

// CheckProjectPermission : permission legacy (slug "project:view"…) via le
// rôle legacy équivalent du membre.
func (r *pgAuthRepo) CheckProjectPermission(ctx context.Context, userID, projectID uuid.UUID, permSlug string) (bool, error) {
	m, err := r.GetProjectMember(ctx, projectID, userID)
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	role, err := domain.RoleTypes.NewFromString(ctx, m.Role.Name)
	if err != nil {
		return false, nil // rôle IAM sans équivalent legacy
	}
	perm, err := domain.PermissionTypes.NewFromString(ctx, permSlug)
	if err != nil {
		return false, nil
	}
	return domain.RoleHasPermission(role, perm), nil
}

// AddProjectMember : accorde au membre le rôle IAM équivalent à son rôle
// legacy, en remplacement de ses rôles précédents sur le projet.
func (r *pgAuthRepo) AddProjectMember(ctx context.Context, member *domain.ProjectMember) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return setMemberRole(ctx, tx, member)
	})
}

// Upsert : même effet qu'AddProjectMember (idempotent).
func (r *pgAuthRepo) Upsert(ctx context.Context, m *domain.ProjectMember) error {
	return r.AddProjectMember(ctx, m)
}

func (r *pgAuthRepo) RemoveProjectMember(ctx context.Context, projectID, userID uuid.UUID) error {
	return iam.RemovePrincipal(ctx, r.db, iam.ProjectName(projectID), iam.UserPrincipal(userID), "system:rbac")
}

func setMemberRole(ctx context.Context, tx *gorm.DB, m *domain.ProjectMember) error {
	project, user := iam.ProjectName(m.ProjectID), iam.UserPrincipal(m.UserID)
	if err := iam.RemovePrincipal(ctx, tx, project, user, "system:rbac"); err != nil {
		return err
	}
	if m.Status != "" && m.Status != domain.ProjectMemberStatusActive && m.Status != domain.ProjectMemberStatusApproved {
		return nil // invitation en attente : aucun droit
	}
	name := strings.TrimSpace(m.Role.Name)
	if name == "" {
		return fmt.Errorf("project member role is required")
	}
	iamRole := domain.LegacyRoleToIAM(name)
	return iam.AddBinding(ctx, tx, iam.Binding{Resource: project, Role: iamRole, Principal: user}, "system:rbac")
}

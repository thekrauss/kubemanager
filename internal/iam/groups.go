package iam

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/audit"
)

// Groupes (iam.md §4) : ensembles de personnes et de comptes de service d'une
// organisation, utilisables comme membre d'une policy ("group:{uuid}") dans
// cette organisation. Pas de groupes imbriqués (pas de cycles, coût borné).
//
// Anti-escalade : ajouter un membre lui donne tous les rôles du groupe ;
// l'appelant doit donc détenir lui-même, sur chaque ressource, toutes les
// permissions des rôles accordés au groupe. Retirer un membre est libre.

var groupIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,61}[a-z0-9]$`)

var (
	ErrInvalidGroupID      = errors.New("group_id must be 3-63 chars: lowercase letters, digits and hyphens, starting with a letter")
	ErrGroupIDTaken        = errors.New("a group with this group_id already exists in the organization")
	ErrGroupNotFound       = errors.New("group not found")
	ErrNestedGroup         = errors.New("a group cannot contain another group")
	ErrGroupMemberNotFound = errors.New("member not found in this group")
	ErrGroupEscalation     = errors.New("this group has roles with permissions you do not have: you cannot add members to it")
	ErrGroupOutOfScope     = errors.New("a group can only be granted roles within its organization")
)

// GroupsOf (GormStore) : groupes actifs dont p est membre.
func (s *GormStore) GroupsOf(ctx context.Context, p Principal) ([]Principal, error) {
	var ids []uuid.UUID
	err := s.DB.WithContext(ctx).Raw(`SELECT g.id FROM iam_group_members m
		JOIN iam_groups g ON g.id = m.group_id
		WHERE m.principal = ? AND g.deleted_at IS NULL`, string(p)).Scan(&ids).Error
	if err != nil {
		return nil, err
	}
	out := make([]Principal, len(ids))
	for i, id := range ids {
		out[i] = GroupPrincipal(id)
	}
	return out, nil
}

// Group : groupe d'une organisation.
type Group struct {
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID      `gorm:"type:uuid" json:"organization_id"`
	GroupID        string         `json:"group_id"`
	DisplayName    string         `json:"display_name"`
	Description    string         `json:"description"`
	CreatedBy      string         `json:"-"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-"`

	Principal   Principal    `gorm:"-" json:"principal"`
	Resource    ResourceName `gorm:"-" json:"resource"`
	MemberCount int          `gorm:"-" json:"member_count"`
}

func (Group) TableName() string { return "iam_groups" }

func (g *Group) decorate() {
	g.Principal = GroupPrincipal(g.ID)
	g.Resource = OrganizationName(g.OrganizationID).Child("groups", g.ID)
}

// GroupMember : membre d'un groupe, avec de quoi l'afficher.
type GroupMember struct {
	Principal   Principal `json:"principal"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	AddedAt     time.Time `json:"added_at"`
}

// GroupService : groupes et appartenance.
type GroupService struct {
	DB     *gorm.DB
	Engine *Engine
}

func (s *GroupService) Create(ctx context.Context, caller Principal, orgID uuid.UUID, groupID, displayName, description string) (*Group, error) {
	groupID = strings.TrimSpace(groupID)
	if !groupIDRe.MatchString(groupID) || strings.Contains(groupID, "--") {
		return nil, ErrInvalidGroupID
	}
	g := &Group{ID: uuid.New(), OrganizationID: orgID, GroupID: groupID, DisplayName: truncate(strings.TrimSpace(displayName), maxDisplayNameLen),
		Description: strings.TrimSpace(description), CreatedBy: string(caller)}
	if err := s.DB.WithContext(ctx).Create(g).Error; err != nil {
		if isUniqueViolation(err) {
			return nil, ErrGroupIDTaken
		}
		return nil, err
	}
	g.decorate()
	audit.Annotate(ctx, "group", g.Principal)
	return g, nil
}

func (s *GroupService) List(ctx context.Context, orgID uuid.UUID) ([]Group, error) {
	var out []Group
	if err := s.DB.WithContext(ctx).Where("organization_id = ?", orgID).Order("group_id").Find(&out).Error; err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	var counts []struct {
		GroupID uuid.UUID
		N       int
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT group_id, count(*) AS n FROM iam_group_members
		WHERE group_id IN ? GROUP BY group_id`, ids).Scan(&counts).Error; err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]int{}
	for _, c := range counts {
		byID[c.GroupID] = c.N
	}
	for i := range out {
		out[i].decorate()
		out[i].MemberCount = byID[out[i].ID]
	}
	return out, nil
}

func (s *GroupService) Get(ctx context.Context, orgID, id uuid.UUID) (*Group, error) {
	var g Group
	err := s.DB.WithContext(ctx).Where("id = ? AND organization_id = ?", id, orgID).First(&g).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, err
	}
	g.decorate()
	return &g, nil
}

func (s *GroupService) Update(ctx context.Context, orgID, id uuid.UUID, displayName, description string) (*Group, error) {
	g, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	g.DisplayName, g.Description = truncate(strings.TrimSpace(displayName), maxDisplayNameLen), strings.TrimSpace(description)
	if err := s.DB.WithContext(ctx).Model(g).Updates(map[string]any{
		"display_name": g.DisplayName, "description": g.Description, "updated_at": time.Now()}).Error; err != nil {
		return nil, err
	}
	return g, nil
}

// Delete : le groupe perd tous ses rôles, partout, puis disparaît.
func (s *GroupService) Delete(ctx context.Context, caller Principal, orgID, id uuid.UUID) error {
	g, err := s.Get(ctx, orgID, id)
	if err != nil {
		return err
	}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var resources []string
		if err := tx.Model(&bindingRow{}).Where("principal = ?", string(g.Principal)).
			Distinct().Pluck("resource_name", &resources).Error; err != nil {
			return err
		}
		for _, r := range resources {
			if err := RemovePrincipal(ctx, tx, ResourceName(r), g.Principal, string(caller)); err != nil {
				return err
			}
		}
		if err := tx.Exec(`DELETE FROM iam_group_members WHERE group_id = ?`, g.ID).Error; err != nil {
			return err
		}
		return tx.Delete(g).Error
	})
	if err != nil {
		return err
	}
	notifyChanged(g.Principal)
	audit.Annotate(ctx, "group", g.Principal)
	return nil
}

func (s *GroupService) ListMembers(ctx context.Context, orgID, id uuid.UUID) ([]GroupMember, error) {
	g, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	var out []GroupMember
	err = s.DB.WithContext(ctx).Raw(`
		SELECT m.principal, COALESCE(u.email, '') AS email,
		       COALESCE(NULLIF(u.full_name, ''), NULLIF(sa.display_name, ''), sa.account_id, '') AS display_name,
		       m.created_at AS added_at
		FROM iam_group_members m
		LEFT JOIN users u ON m.principal = 'user:' || u.id::text
		LEFT JOIN service_accounts sa ON m.principal = 'serviceAccount:' || sa.id::text
		WHERE m.group_id = ? ORDER BY m.created_at`, g.ID).Scan(&out).Error
	return out, err
}

// AddMember : ajoute une personne ("user:<uuid>" ou "user:<email>") ou un
// compte de service. Idempotent.
func (s *GroupService) AddMember(ctx context.Context, caller Principal, orgID, id uuid.UUID, member Principal) (*GroupMember, error) {
	g, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveMember(ctx, s.DB, member)
	if err != nil {
		return nil, err
	}
	if kind, _, _ := resolved.Parse(); kind != KindUser && kind != KindServiceAccount {
		if kind == KindGroup {
			return nil, ErrNestedGroup
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidPrincipal, member)
	}
	if err := s.checkNoEscalation(ctx, caller, g); err != nil {
		return nil, err
	}
	if err := s.DB.WithContext(ctx).Exec(`INSERT INTO iam_group_members (group_id, principal, created_by)
		VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, g.ID, string(resolved), string(caller)).Error; err != nil {
		return nil, err
	}
	notifyChanged(resolved)
	audit.Annotate(ctx, "group", g.Principal)
	audit.Annotate(ctx, "member", string(resolved))
	return &GroupMember{Principal: resolved, AddedAt: time.Now()}, nil
}

func (s *GroupService) RemoveMember(ctx context.Context, orgID, id uuid.UUID, member Principal) error {
	g, err := s.Get(ctx, orgID, id)
	if err != nil {
		return err
	}
	// Même désignation qu'à l'ajout (user:<email> → user:<uuid>) : avant, un
	// membre ajouté par son email ne pouvait pas être retiré par son email
	// (404) et gardait ses accès. Membre introuvable (compte supprimé) : tel quel.
	if resolved, err := resolveMember(ctx, s.DB, member); err == nil {
		member = resolved
	}
	res := s.DB.WithContext(ctx).Exec(`DELETE FROM iam_group_members WHERE group_id = ? AND principal = ?`, g.ID, string(member))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrGroupMemberNotFound
	}
	notifyChanged(member)
	audit.Annotate(ctx, "group", g.Principal)
	audit.Annotate(ctx, "member", string(member))
	return nil
}

// checkNoEscalation : caller détient, sur chaque ressource, toutes les
// permissions des rôles accordés au groupe.
func (s *GroupService) checkNoEscalation(ctx context.Context, caller Principal, g *Group) error {
	var rows []bindingRow
	if err := s.DB.WithContext(ctx).Where("principal = ?", string(g.Principal)).Find(&rows).Error; err != nil {
		return err
	}
	for _, b := range rows {
		role, ok, err := resolveRole(ctx, s.DB, b.Role)
		if err != nil {
			return err
		}
		if !ok {
			continue // rôle disparu : n'accorde rien
		}
		granted, err := s.Engine.TestPermissions(ctx, caller, ResourceName(b.ResourceName), role.Permissions)
		if err != nil {
			return err
		}
		if len(granted) != len(role.Permissions) {
			return fmt.Errorf("%w: %s", ErrGroupEscalation, b.Role+" @ "+b.ResourceName)
		}
	}
	return nil
}

// groupOrganization : organisation d'un groupe actif ; ErrUnknownMember sinon.
func groupOrganization(ctx context.Context, db *gorm.DB, id uuid.UUID) (uuid.UUID, error) {
	var org uuid.UUID
	err := db.WithContext(ctx).Raw(`SELECT organization_id FROM iam_groups WHERE id = ? AND deleted_at IS NULL`, id).
		Row().Scan(&org)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: %s", ErrUnknownMember, GroupPrincipal(id))
	}
	return org, err
}

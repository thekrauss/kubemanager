package iam

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/audit"
)

// Rôles personnalisés (iam.md §6.1) : définis par un client sur une
// organisation ou un projet, attribuables sur cette ressource et ses
// descendantes, limités aux permissions marquées CustomRoles.
//
//	organizations/{o}/roles/{id}   projects/{p}/roles/{id}
//
// Règles :
//   - créer un rôle ou lui ajouter des permissions exige de détenir soi-même
//     ces permissions sur le parent (sinon : se donner un droit en modifiant
//     un rôle qu'on a déjà) ;
//   - modifier un rôle change immédiatement les droits de tous ceux qui l'ont ;
//   - supprimer un rôle retire aussi tous ses bindings (un nom réutilisé ne
//     ressuscite aucun droit).

var customRoleNameRe = regexp.MustCompile(`^(organizations|projects)/([0-9a-f-]{36})/roles/([A-Za-z0-9_.]{3,64})$`)
var roleIDRe = regexp.MustCompile(`^[A-Za-z0-9_.]{3,64}$`)

// MaxCustomRolePermissions : borne raisonnable d'un rôle.
const MaxCustomRolePermissions = 300

var (
	ErrInvalidRoleID             = errors.New("role_id must be 3-64 chars: letters, digits, '_' or '.'")
	ErrRoleNotFound              = errors.New("custom role not found")
	ErrRoleIDTaken               = errors.New("a custom role with this role_id already exists here")
	ErrInvalidRolePermissions    = errors.New("invalid permissions for a custom role")
	ErrCustomRoleParent          = errors.New("custom roles live on an organization or a project")
	ErrCustomRoleOutOfScope      = errors.New("this custom role can only be granted on its organization or project and their resources")
	ErrRoleEtagMismatch          = errors.New("the role was modified concurrently: reload it and retry")
	ErrCustomRoleTitleRequired   = errors.New("title is required (at most 100 characters)")
	ErrCustomRoleEscalation      = errors.New("you cannot put in a role permissions you do not have")
	ErrCustomRolePermissionCount = errors.New("a custom role has between 1 and 300 permissions")
)

// IsCustomRoleName : forme "organizations|projects/{uuid}/roles/{id}".
func IsCustomRoleName(name string) bool { return customRoleNameRe.MatchString(name) }

// CustomRoleParent : ressource parente d'un nom de rôle personnalisé.
func CustomRoleParent(name string) (ResourceName, bool) {
	m := customRoleNameRe.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return ResourceName(m[1] + "/" + m[2]), true
}

// customRoleRow : table iam_custom_roles.
type customRoleRow struct {
	Name           string `gorm:"primaryKey"`
	ParentResource string
	RoleID         string
	Title          string
	Description    string
	Permissions    pq.StringArray `gorm:"type:text[]"`
	Etag           string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (customRoleRow) TableName() string { return "iam_custom_roles" }

func (r customRoleRow) role() Role {
	perms := make([]Permission, len(r.Permissions))
	for i, p := range r.Permissions {
		perms[i] = Permission(p)
	}
	return Role{Name: r.Name, Title: r.Title, Description: r.Description, Permissions: perms}
}

// CustomRole : vue API d'un rôle personnalisé.
type CustomRole struct {
	Name        string       `json:"name"`
	Parent      ResourceName `json:"parent"`
	RoleID      string       `json:"role_id"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Permissions []string     `json:"permissions"`
	Etag        string       `json:"etag"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

func (r customRoleRow) view() CustomRole {
	return CustomRole{Name: r.Name, Parent: ResourceName(r.ParentResource), RoleID: r.RoleID, Title: r.Title,
		Description: r.Description, Permissions: append([]string{}, r.Permissions...), Etag: r.Etag,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// CustomRoles (GormStore) : rôles personnalisés existants parmi names.
func (s *GormStore) CustomRoles(ctx context.Context, names []string) (map[string]Role, error) {
	return loadCustomRoles(ctx, s.DB, names)
}

func loadCustomRoles(ctx context.Context, db *gorm.DB, names []string) (map[string]Role, error) {
	out := map[string]Role{}
	if len(names) == 0 {
		return out, nil
	}
	var rows []customRoleRow
	if err := db.WithContext(ctx).Where("name IN ?", names).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Name] = r.role()
	}
	return out, nil
}

// resolveRole : rôle prédéfini ou personnalisé existant.
func resolveRole(ctx context.Context, db *gorm.DB, name string) (Role, bool, error) {
	if r, ok := LookupRole(name); ok {
		return r, true, nil
	}
	if !IsCustomRoleName(name) {
		return Role{}, false, nil
	}
	roles, err := loadCustomRoles(ctx, db, []string{name})
	if err != nil {
		return Role{}, false, err
	}
	r, ok := roles[name]
	return r, ok, nil
}

// CustomRoleService : gestion des rôles personnalisés.
type CustomRoleService struct {
	DB     *gorm.DB
	Engine *Engine
}

// CustomRoleInput : création ou mise à jour.
type CustomRoleInput struct {
	Title       string   `json:"title" description:"Nom affiché (100 caractères max)"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions" description:"Permissions incluses (utilisables en rôle personnalisé, voir GET /iam/permissions)"`
}

func validatePermissions(raw []string) ([]string, error) {
	if len(raw) == 0 || len(raw) > MaxCustomRolePermissions {
		return nil, ErrCustomRolePermissionCount
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		info, ok := Info(Permission(p))
		if !ok || !info.CustomRoles || info.PlatformOnly {
			return nil, fmt.Errorf("%w: %q", ErrInvalidRolePermissions, p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func customParent(parent ResourceName) error {
	if parent.Validate() != nil || !needsOwner(parent) {
		return ErrCustomRoleParent
	}
	return nil
}

// requireHeld : caller doit détenir chacune de perms sur parent.
func (s *CustomRoleService) requireHeld(ctx context.Context, caller Principal, parent ResourceName, perms []string) error {
	if len(perms) == 0 {
		return nil
	}
	list := make([]Permission, len(perms))
	for i, p := range perms {
		list[i] = Permission(p)
	}
	granted, err := s.Engine.TestPermissions(ctx, caller, parent, list)
	if err != nil {
		return err
	}
	if len(granted) != len(list) {
		return ErrCustomRoleEscalation
	}
	return nil
}

func (s *CustomRoleService) Create(ctx context.Context, caller Principal, parent ResourceName, roleID string, in CustomRoleInput) (*CustomRole, error) {
	if err := customParent(parent); err != nil {
		return nil, err
	}
	if !roleIDRe.MatchString(roleID) {
		return nil, ErrInvalidRoleID
	}
	title := strings.TrimSpace(in.Title)
	if title == "" || len([]rune(title)) > maxRoleTitleLen {
		return nil, ErrCustomRoleTitleRequired
	}
	perms, err := validatePermissions(in.Permissions)
	if err != nil {
		return nil, err
	}
	if err := s.requireHeld(ctx, caller, parent, perms); err != nil {
		return nil, err
	}
	etag, err := newEtag()
	if err != nil {
		return nil, err
	}
	row := customRoleRow{Name: string(parent) + CustomRoleSegment + roleID, ParentResource: string(parent), RoleID: roleID,
		Title: title, Description: strings.TrimSpace(in.Description), Permissions: perms, Etag: etag,
		CreatedBy: string(caller)}
	if err := s.DB.WithContext(ctx).Create(&row).Error; err != nil {
		if isUniqueViolation(err) {
			return nil, ErrRoleIDTaken
		}
		return nil, err
	}
	audit.Annotate(ctx, "role", row.Name)
	audit.Annotate(ctx, "permissions", perms)
	v := row.view()
	return &v, nil
}

func (s *CustomRoleService) List(ctx context.Context, parent ResourceName) ([]CustomRole, error) {
	if err := customParent(parent); err != nil {
		return nil, err
	}
	var rows []customRoleRow
	if err := s.DB.WithContext(ctx).Where("parent_resource = ?", string(parent)).Order("role_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]CustomRole, len(rows))
	for i, r := range rows {
		out[i] = r.view()
	}
	return out, nil
}

func (s *CustomRoleService) Get(ctx context.Context, name string) (*CustomRole, error) {
	var row customRoleRow
	err := s.DB.WithContext(ctx).Where("name = ?", name).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, err
	}
	v := row.view()
	return &v, nil
}

// Update : remplace titre, description et permissions (etag obligatoire).
// Les permissions ajoutées doivent être détenues par l'appelant.
func (s *CustomRoleService) Update(ctx context.Context, caller Principal, name, etag string, in CustomRoleInput) (*CustomRole, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" || len([]rune(title)) > maxRoleTitleLen {
		return nil, ErrCustomRoleTitleRequired
	}
	perms, err := validatePermissions(in.Permissions)
	if err != nil {
		return nil, err
	}
	var updated customRoleRow
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row customRoleRow
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where("name = ?", name).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRoleNotFound
		}
		if err != nil {
			return err
		}
		if etag == "" || etag != row.Etag {
			return ErrRoleEtagMismatch
		}
		had := map[string]bool{}
		for _, p := range row.Permissions {
			had[p] = true
		}
		var added []string
		for _, p := range perms {
			if !had[p] {
				added = append(added, p)
			}
		}
		if err := s.requireHeld(ctx, caller, ResourceName(row.ParentResource), added); err != nil {
			return err
		}
		next, err := newEtag()
		if err != nil {
			return err
		}
		row.Title, row.Description, row.Permissions = title, strings.TrimSpace(in.Description), perms
		row.Etag, row.UpdatedAt = next, time.Now()
		audit.Annotate(ctx, "role", row.Name)
		audit.Annotate(ctx, "permissions_added", added)
		updated = row
		return tx.Save(&row).Error
	})
	if err != nil {
		return nil, err
	}
	notifyChanged(AllUsers) // les droits de tous les détenteurs changent
	v := updated.view()
	return &v, nil
}

// Delete : supprime le rôle et tous ses bindings.
func (s *CustomRoleService) Delete(ctx context.Context, caller Principal, name string) error {
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("name = ?", name).Delete(&customRoleRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrRoleNotFound
		}
		var resources []string
		if err := tx.Model(&bindingRow{}).Where("role = ?", name).Distinct().Pluck("resource_name", &resources).Error; err != nil {
			return err
		}
		if err := tx.Where("role = ?", name).Delete(&bindingRow{}).Error; err != nil {
			return err
		}
		for _, r := range resources {
			if err := touchPolicy(ctx, tx, ResourceName(r), string(caller)); err != nil {
				return err
			}
		}
		audit.Annotate(ctx, "role", name)
		audit.Annotate(ctx, "bindings_removed_on", resources)
		return nil
	})
	if err != nil {
		return err
	}
	notifyChanged(AllUsers)
	return nil
}

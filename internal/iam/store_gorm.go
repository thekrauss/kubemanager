package iam

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GormStore : implémentation Postgres de Store.
type GormStore struct {
	DB *gorm.DB
}

type bindingRow struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ResourceName string
	Role         string
	Principal    string
	Condition    []byte `gorm:"type:jsonb"`
	CreatedAt    time.Time
	CreatedBy    string
}

func (bindingRow) TableName() string { return "iam_bindings" }

type policyRow struct {
	ResourceName string `gorm:"primaryKey"`
	Etag         string
	Version      int
	UpdatedAt    time.Time
	UpdatedBy    string
}

func (policyRow) TableName() string { return "iam_policies" }

func (s *GormStore) BindingsFor(ctx context.Context, principals []Principal) ([]Binding, error) {
	keys := make([]string, len(principals))
	for i, p := range principals {
		keys[i] = string(p)
	}
	var rows []bindingRow
	if err := s.DB.WithContext(ctx).Where("principal IN ?", keys).Find(&rows).Error; err != nil {
		return nil, err
	}
	return decodeBindings(rows)
}

// decodeBindings : lignes → bindings. Une condition illisible devient une
// condition invalide : le binding n'accorde rien (échec fermé).
func decodeBindings(rows []bindingRow) ([]Binding, error) {
	out := make([]Binding, 0, len(rows))
	for _, r := range rows {
		b := Binding{Resource: ResourceName(r.ResourceName), Role: r.Role, Principal: Principal(r.Principal)}
		if len(r.Condition) > 0 {
			var c Condition
			if err := json.Unmarshal(r.Condition, &c); err != nil {
				c = Condition{Title: "invalid", Expression: "invalid"}
			}
			b.Condition = &c
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *GormStore) ProjectOrganization(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	var org uuid.UUID
	err := s.DB.WithContext(ctx).Raw(
		`SELECT organization_id FROM projects WHERE id = ? AND deleted_at IS NULL`, projectID).Row().Scan(&org)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, ErrResourceNotFound
		}
		return uuid.Nil, err
	}
	return org, nil
}

// ── Écritures ────────────────────────────────────────────────────────────
// Fonctions sur *gorm.DB pour se composer avec les transactions des modules
// (ex : création d'un projet + binding owner, atomiquement).

// ErrInvalidBinding : binding refusé (rôle inconnu, rôle non attribuable ici…).
var ErrInvalidBinding = errors.New("invalid binding")

// ValidateBinding : contrôle un binding avant écriture.
func ValidateBinding(b Binding) error {
	if err := b.Resource.Validate(); err != nil {
		return err
	}
	if err := b.Principal.Validate(); err != nil {
		return err
	}
	if IsCustomRoleName(b.Role) {
		// Existence et portée vérifiées par PolicyService (base, ancêtres).
		return ValidateCondition(b.Condition)
	}
	role, ok := LookupRole(b.Role)
	if !ok {
		return fmt.Errorf("%w: unknown role %q", ErrInvalidBinding, b.Role)
	}
	if role.PlatformOnly != (b.Resource == Platform) {
		return fmt.Errorf("%w: role %q cannot be granted on %s", ErrInvalidBinding, b.Role, b.Resource)
	}
	return ValidateCondition(b.Condition)
}

// AddBinding : accorde un rôle (idempotent) et fait tourner l'etag de la policy.
func AddBinding(ctx context.Context, db *gorm.DB, b Binding, actor string) error {
	if err := ValidateBinding(b); err != nil {
		return err
	}
	row := bindingRow{ResourceName: string(b.Resource), Role: b.Role, Principal: string(b.Principal), CreatedBy: actor}
	if b.Condition != nil {
		raw, err := json.Marshal(b.Condition)
		if err != nil {
			return err
		}
		row.Condition = raw
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	if err := touchPolicy(ctx, db, b.Resource, actor); err != nil {
		return err
	}
	notifyChanged(b.Principal)
	return nil
}

// RemovePrincipal : retire tous les rôles de p sur la ressource.
func RemovePrincipal(ctx context.Context, db *gorm.DB, r ResourceName, p Principal, actor string) error {
	if err := db.WithContext(ctx).
		Where("resource_name = ? AND principal = ?", string(r), string(p)).
		Delete(&bindingRow{}).Error; err != nil {
		return err
	}
	if err := touchPolicy(ctx, db, r, actor); err != nil {
		return err
	}
	notifyChanged(p)
	return nil
}

// DeleteResourcePolicy : supprime la policy d'une ressource et de ses
// sous-ressources (ressource supprimée).
func DeleteResourcePolicy(ctx context.Context, db *gorm.DB, r ResourceName) error {
	prefix := string(r) + "/%"
	if err := db.WithContext(ctx).
		Where("resource_name = ? OR resource_name LIKE ?", string(r), prefix).
		Delete(&bindingRow{}).Error; err != nil {
		return err
	}
	if err := db.WithContext(ctx).
		Where("resource_name = ? OR resource_name LIKE ?", string(r), prefix).
		Delete(&policyRow{}).Error; err != nil {
		return err
	}
	notifyChanged(AllUsers) // principals non connus ici : invalidation globale
	return nil
}

func touchPolicy(ctx context.Context, db *gorm.DB, r ResourceName, actor string) error {
	etag, err := newEtag()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "resource_name"}},
		DoUpdates: clause.Assignments(map[string]any{
			"etag":       etag,
			"version":    gorm.Expr("iam_policies.version + 1"),
			"updated_at": time.Now(),
			"updated_by": actor,
		}),
	}).Create(&policyRow{ResourceName: string(r), Etag: etag, Version: 1, UpdatedAt: time.Now(), UpdatedBy: actor}).Error
}

func newEtag() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

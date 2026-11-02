// Package organizations : racine de la hiérarchie de ressources
// (docs/architecture/iam.md §3). Chaque utilisateur a une organisation
// personnelle, créée à la première occasion (inscription, premier projet).
package organizations

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/iam"
)

const (
	KindPersonal = "PERSONAL"
	KindTeam     = "TEAM"
)

// defaultPersonalOrgName : nom de l'organisation personnelle créée sans nom.
const defaultPersonalOrgName = "Mon organisation"

type Organization struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	DisplayName string         `gorm:"not null" json:"display_name"`
	Kind        string         `gorm:"not null;default:'TEAM'" json:"kind"`
	OwnerUserID *uuid.UUID     `gorm:"type:uuid" json:"-"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// EnsurePersonal renvoie l'organisation personnelle de l'utilisateur, en la
// créant au besoin avec un binding roles/owner. Idempotent et sûr en
// concurrence (index unique partiel sur owner_user_id).
func EnsurePersonal(ctx context.Context, db *gorm.DB, userID uuid.UUID, displayName string) (uuid.UUID, error) {
	if id, err := findPersonal(ctx, db, userID); err == nil {
		return id, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return uuid.Nil, err
	}

	if displayName == "" {
		displayName = defaultPersonalOrgName
	}
	org := Organization{ID: uuid.New(), DisplayName: displayName, Kind: KindPersonal, OwnerUserID: &userID}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&org).Error; err != nil {
			return err
		}
		return iam.AddBinding(ctx, tx, iam.Binding{
			Resource:  iam.OrganizationName(org.ID),
			Role:      iam.RoleOwner,
			Principal: iam.UserPrincipal(userID),
		}, iam.ActorSystemOrganizations)
	})
	if err != nil {
		// Créée entre-temps par une requête concurrente : on la relit.
		if id, findErr := findPersonal(ctx, db, userID); findErr == nil {
			return id, nil
		}
		return uuid.Nil, err
	}
	return org.ID, nil
}

func findPersonal(ctx context.Context, db *gorm.DB, userID uuid.UUID) (uuid.UUID, error) {
	var org Organization
	err := db.WithContext(ctx).
		Where("kind = ? AND owner_user_id = ?", KindPersonal, userID).
		First(&org).Error
	return org.ID, err
}

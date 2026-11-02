package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Comptes Google / GitHub liés à un compte betorun.

var ErrIdentityNotFound = errors.New("no linked account for this provider")

type identityRow struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID     uuid.UUID `gorm:"type:uuid"`
	Provider   string
	Subject    string
	Email      string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

func (identityRow) TableName() string { return "user_identities" }

// LinkedIdentity : compte lié, tel que montré à son propriétaire.
type LinkedIdentity struct {
	Provider   string    `json:"provider"`
	Email      string    `json:"email"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

type Identities struct {
	DB *gorm.DB
}

// Find : compte betorun lié à cette identité (uuid.Nil : aucun).
func (i *Identities) Find(ctx context.Context, provider, subject string) (uuid.UUID, error) {
	var row identityRow
	err := i.DB.WithContext(ctx).Where("provider = ? AND subject = ?", provider, subject).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	_ = i.DB.WithContext(ctx).Model(&row).Update("last_used_at", time.Now()).Error
	return row.UserID, nil
}

// Link : lie l'identité au compte (une seule par fournisseur et par compte).
func (i *Identities) Link(ctx context.Context, userID uuid.UUID, provider, subject, email string) error {
	now := time.Now()
	return i.DB.WithContext(ctx).Exec(`INSERT INTO user_identities (user_id, provider, subject, email, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (user_id, provider) DO UPDATE
		SET subject = EXCLUDED.subject, email = EXCLUDED.email, last_used_at = EXCLUDED.last_used_at`,
		userID, provider, subject, email, now, now).Error
}

func (i *Identities) List(ctx context.Context, userID uuid.UUID) ([]LinkedIdentity, error) {
	var rows []identityRow
	if err := i.DB.WithContext(ctx).Where("user_id = ?", userID).Order("provider").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]LinkedIdentity, 0, len(rows))
	for _, r := range rows {
		out = append(out, LinkedIdentity{Provider: r.Provider, Email: r.Email, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt})
	}
	return out, nil
}

func (i *Identities) Unlink(ctx context.Context, userID uuid.UUID, provider string) error {
	res := i.DB.WithContext(ctx).Where("user_id = ? AND provider = ?", userID, provider).Delete(&identityRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrIdentityNotFound
	}
	return nil
}

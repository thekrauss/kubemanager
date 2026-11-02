package auth

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Liens envoyés par email.
const (
	PurposeVerifyEmail   = "verify_email"
	PurposeResetPassword = "reset_password"

	VerifyEmailTTL   = 24 * time.Hour
	ResetPasswordTTL = time.Hour
)

type emailTokenRow struct {
	TokenHash []byte `gorm:"primaryKey"`
	Subject   string
	Email     string
	Purpose   string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

func (emailTokenRow) TableName() string { return "auth_email_tokens" }

// EmailLink : compte désigné par un lien valide.
type EmailLink struct {
	Subject string // identifiant d'annuaire
	Email   string
}

// EmailTokens : jetons à usage unique des liens envoyés par email (seule
// leur empreinte est stockée).
type EmailTokens struct {
	DB  *gorm.DB
	Now func() time.Time
}

func (e *EmailTokens) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Issue : nouveau lien ; les liens précédents de même usage pour ce compte
// cessent de valoir (seul le dernier email reçu fonctionne).
func (e *EmailTokens) Issue(ctx context.Context, subject, email, purpose string, ttl time.Duration) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	now := e.now()
	err = e.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&emailTokenRow{}).Where("subject = ? AND purpose = ? AND used_at IS NULL", subject, purpose).
			Update("used_at", now).Error; err != nil {
			return err
		}
		return tx.Create(&emailTokenRow{TokenHash: hashToken(tok), Subject: subject, Email: email, Purpose: purpose,
			ExpiresAt: now.Add(ttl), CreatedAt: now}).Error
	})
	return tok, err
}

// Consume : lien valide, non expiré, de cet usage ; il ne servira plus.
func (e *EmailTokens) Consume(ctx context.Context, token, purpose string) (*EmailLink, error) {
	now := e.now()
	var rows []emailTokenRow
	err := e.DB.WithContext(ctx).Raw(`UPDATE auth_email_tokens SET used_at = ?
		WHERE token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > ? RETURNING *`,
		now, hashToken(token), purpose, now).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrEmailLinkInvalid
	}
	return &EmailLink{Subject: rows[0].Subject, Email: rows[0].Email}, nil
}

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	factorTOTP = "totp"
	// RecoveryCodeCount : codes de secours remis à l'activation.
	RecoveryCodeCount = 10
	// recoveryAlphabet : sans caractères ambigus (0/O, 1/I/L).
	recoveryAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	recoveryLen      = 10
)

// Méthode qui a servi à vérifier un code.
const (
	MethodTOTP     = "totp"
	MethodRecovery = "recovery"
)

type factorRow struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID       uuid.UUID `gorm:"type:uuid"`
	Kind         string
	SecretEnc    []byte
	ConfirmedAt  *time.Time
	LastUsedStep int64
	CreatedAt    time.Time
}

func (factorRow) TableName() string { return "auth_factors" }

type recoveryRow struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    uuid.UUID `gorm:"type:uuid"`
	CodeHash  []byte
	UsedAt    *time.Time
	CreatedAt time.Time
}

func (recoveryRow) TableName() string { return "auth_recovery_codes" }

// FactorStatus : état du second facteur d'un compte.
type FactorStatus struct {
	TOTP              bool       `json:"totp"`
	TOTPSince         *time.Time `json:"totp_since,omitempty"`
	RecoveryRemaining int        `json:"recovery_codes_remaining"`
}

// TOTPEnrollment : à afficher une seule fois (QR code et saisie manuelle).
type TOTPEnrollment struct {
	URI    string `json:"otpauth_uri"`
	Secret string `json:"secret"`
}

// Factors : second facteur des comptes.
type Factors struct {
	DB     *gorm.DB
	Sealer *Sealer
	Pepper []byte
	Now    func() time.Time
}

func (f *Factors) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func associated(userID uuid.UUID) string { return "totp:" + userID.String() }

func (f *Factors) Status(ctx context.Context, userID uuid.UUID) (*FactorStatus, error) {
	st := &FactorStatus{}
	var row factorRow
	err := f.DB.WithContext(ctx).Where("user_id = ? AND kind = ? AND confirmed_at IS NOT NULL", userID, factorTOTP).
		First(&row).Error
	switch {
	case err == nil:
		st.TOTP, st.TOTPSince = true, row.ConfirmedAt
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}
	var n int64
	if err := f.DB.WithContext(ctx).Model(&recoveryRow{}).Where("user_id = ? AND used_at IS NULL", userID).
		Count(&n).Error; err != nil {
		return nil, err
	}
	st.RecoveryRemaining = int(n)
	return st, nil
}

// HasFactor : le compte a-t-il un second facteur actif ?
func (f *Factors) HasFactor(ctx context.Context, userID uuid.UUID) (bool, error) {
	st, err := f.Status(ctx, userID)
	if err != nil {
		return false, err
	}
	return st.TOTP, nil
}

// StartTOTP : nouveau secret, inactif tant qu'un premier code n'a pas été
// vérifié (ConfirmTOTP). Remplace un enrôlement non terminé ; refusé si un
// TOTP est déjà actif (le désactiver d'abord).
func (f *Factors) StartTOTP(ctx context.Context, userID uuid.UUID, account string) (*TOTPEnrollment, error) {
	if active, err := f.HasFactor(ctx, userID); err != nil {
		return nil, err
	} else if active {
		return nil, ErrFactorExists
	}
	secret, err := newTOTPSecret()
	if err != nil {
		return nil, err
	}
	sealed, err := f.Sealer.Seal(secret, associated(userID))
	if err != nil {
		return nil, err
	}
	err = f.DB.WithContext(ctx).Exec(`INSERT INTO auth_factors (user_id, kind, secret_enc, created_at)
		VALUES (?, ?, ?, ?) ON CONFLICT (user_id, kind) DO UPDATE
		SET secret_enc = EXCLUDED.secret_enc, created_at = EXCLUDED.created_at, last_used_step = 0
		WHERE auth_factors.confirmed_at IS NULL`, userID, factorTOTP, sealed, f.now()).Error
	if err != nil {
		return nil, err
	}
	return &TOTPEnrollment{URI: TOTPURI(secret, account), Secret: FormatTOTPSecret(secret)}, nil
}

// ConfirmTOTP : active le TOTP si code est correct et remet les codes de
// secours (affichés une seule fois).
func (f *Factors) ConfirmTOTP(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	var codes []string
	err := f.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row factorRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND kind = ? AND confirmed_at IS NULL", userID, factorTOTP).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoFactor
		}
		if err != nil {
			return err
		}
		step, err := f.match(&row, code)
		if err != nil {
			return err
		}
		now := f.now()
		if err := tx.Model(&row).Updates(map[string]any{"confirmed_at": now, "last_used_step": step}).Error; err != nil {
			return err
		}
		codes, err = f.replaceRecovery(tx, userID)
		return err
	})
	return codes, err
}

// DisableTOTP : retire le second facteur et ses codes de secours.
func (f *Factors) DisableTOTP(ctx context.Context, userID uuid.UUID) error {
	return f.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("user_id = ? AND kind = ?", userID, factorTOTP).Delete(&factorRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNoFactor
		}
		return tx.Where("user_id = ?", userID).Delete(&recoveryRow{}).Error
	})
}

// RegenerateRecovery : nouveaux codes de secours ; les anciens cessent de valoir.
func (f *Factors) RegenerateRecovery(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if active, err := f.HasFactor(ctx, userID); err != nil {
		return nil, err
	} else if !active {
		return nil, ErrNoFactor
	}
	var codes []string
	err := f.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		codes, err = f.replaceRecovery(tx, userID)
		return err
	})
	return codes, err
}

// Verify : code TOTP ou code de secours (usage unique dans les deux cas).
func (f *Factors) Verify(ctx context.Context, userID uuid.UUID, code string) (string, error) {
	code = strings.TrimSpace(code)
	var method string
	err := f.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row factorRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND kind = ? AND confirmed_at IS NOT NULL", userID, factorTOTP).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoFactor
		}
		if err != nil {
			return err
		}
		if step, err := f.match(&row, code); err == nil {
			method = MethodTOTP
			return tx.Model(&row).Update("last_used_step", step).Error
		} else if !errors.Is(err, ErrInvalidCode) {
			return err
		}
		used, err := f.useRecovery(tx, userID, code)
		if err != nil {
			return err
		}
		if !used {
			return ErrInvalidCode
		}
		method = MethodRecovery
		return nil
	})
	return method, err
}

func (f *Factors) match(row *factorRow, code string) (int64, error) {
	secret, err := f.Sealer.Open(row.SecretEnc, associated(row.UserID))
	if err != nil {
		return 0, err
	}
	step := totpMatch(secret, code, f.now(), row.LastUsedStep)
	if step == 0 {
		return 0, ErrInvalidCode
	}
	return step, nil
}

func normalizeRecovery(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
}

func (f *Factors) useRecovery(tx *gorm.DB, userID uuid.UUID, code string) (bool, error) {
	norm := normalizeRecovery(code)
	if len(norm) != recoveryLen {
		return false, nil
	}
	want := keyedHash(f.Pepper, norm)
	var rows []recoveryRow
	if err := tx.Where("user_id = ? AND used_at IS NULL", userID).Find(&rows).Error; err != nil {
		return false, err
	}
	for _, r := range rows {
		if subtle.ConstantTimeCompare(r.CodeHash, want) == 1 {
			res := tx.Model(&recoveryRow{}).Where("id = ? AND used_at IS NULL", r.ID).Update("used_at", f.now())
			return res.RowsAffected == 1, res.Error
		}
	}
	return false, nil
}

func (f *Factors) replaceRecovery(tx *gorm.DB, userID uuid.UUID) ([]string, error) {
	if err := tx.Where("user_id = ?", userID).Delete(&recoveryRow{}).Error; err != nil {
		return nil, err
	}
	codes := make([]string, 0, RecoveryCodeCount)
	now := f.now()
	for range RecoveryCodeCount {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		if err := tx.Create(&recoveryRow{UserID: userID, CodeHash: keyedHash(f.Pepper, code), CreatedAt: now}).Error; err != nil {
			return nil, err
		}
		codes = append(codes, code[:5]+"-"+code[5:])
	}
	return codes, nil
}

func newRecoveryCode() (string, error) {
	b := make([]byte, recoveryLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, recoveryLen)
	for i, v := range b {
		// 256 % 31 ≠ 0 : biais négligeable (≈ 0,4 %) pour des codes à usage unique.
		out[i] = recoveryAlphabet[int(v)%len(recoveryAlphabet)]
	}
	return string(out), nil
}

package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

type pgAuthRepo struct {
	db *gorm.DB
}

func NewAuthRepository(db *gorm.DB) AuthRepository {
	return &pgAuthRepo{db: db}
}

func (r *pgAuthRepo) CreateUser(ctx context.Context, user *domain.User) error {
	result := r.db.WithContext(ctx).Create(user)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (r *pgAuthRepo) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	result := r.db.WithContext(ctx).Where("email = ?", email).First(&user)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, result.Error
	}
	return &user, nil
}

func (r *pgAuthRepo) GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var user domain.User
	result := r.db.WithContext(ctx).First(&user, "id = ?", id)
	if result.Error != nil {
		return nil, result.Error
	}
	return &user, nil
}

func (r *pgAuthRepo) UpdateUser(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Save(user).Error
}

func (r *pgAuthRepo) CreateSession(ctx context.Context, session *domain.UserSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *pgAuthRepo) GetSessionByID(ctx context.Context, id uuid.UUID) (*domain.UserSession, error) {
	var session domain.UserSession
	err := r.db.WithContext(ctx).
		Where("id = ? AND is_blocked = false AND expires_at > NOW()", id).
		First(&session).Error
	return &session, err
}

func (r *pgAuthRepo) RevokeSession(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.UserSession{}).
		Where("id = ?", id).
		Update("is_blocked", true).Error
}

func (r *pgAuthRepo) RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.UserSession{}).
		Where("user_id = ?", userID).
		Update("is_blocked", true).Error
}

func (r *pgAuthRepo) CreateAPIKey(ctx context.Context, key *domain.APIKey) error {
	return r.db.WithContext(ctx).Create(key).Error
}

func (r *pgAuthRepo) GetAPIKeyByPrefix(ctx context.Context, prefix string) (*domain.APIKey, error) {
	var key domain.APIKey
	err := r.db.WithContext(ctx).
		Where("prefix = ?", prefix).
		First(&key).Error
	if err != nil {
		return nil, err
	}
	return &key, nil
}

func (r *pgAuthRepo) ListUserAPIKeys(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error) {
	var keys []domain.APIKey
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Find(&keys).Error
	return keys, err
}

func (r *pgAuthRepo) RevokeAPIKey(ctx context.Context, keyID uuid.UUID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", keyID, userID).
		Delete(&domain.APIKey{}).Error
}

func (r *pgAuthRepo) ListAPIKeysByKind(ctx context.Context, kind string) ([]domain.APIKey, error) {
	var keys []domain.APIKey
	err := r.db.WithContext(ctx).
		Where("kind = ?", kind).
		Order("created_at DESC").
		Find(&keys).Error
	return keys, err
}

func (r *pgAuthRepo) UpdateAPIKeyExpiration(ctx context.Context, keyID uuid.UUID, expiresAt *time.Time) error {
	return r.db.WithContext(ctx).
		Model(&domain.APIKey{}).
		Where("id = ?", keyID).
		Update("expires_at", expiresAt).Error
}

// GetAPIKeyByID retourne une clé unique (pour introspection). Le user_id est
// vérifié côté service pour garantir qu'un user ne peut pas lire la clé d'un autre.
func (r *pgAuthRepo) GetAPIKeyByID(ctx context.Context, keyID uuid.UUID) (*domain.APIKey, error) {
	var key domain.APIKey
	err := r.db.WithContext(ctx).Where("id = ?", keyID).First(&key).Error
	if err != nil {
		return nil, err
	}
	return &key, nil
}

func (r *pgAuthRepo) UpdateAPIKeyUsage(ctx context.Context, keyID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&domain.APIKey{}).
		Where("id = ?", keyID).
		Updates(map[string]interface{}{
			"last_used_at": &now,
			"usage_count":  gorm.Expr("usage_count + 1"),
		}).Error
}

// BatchIncrementAPIKeyUsage : update groupé via "UPDATE ... FROM (VALUES ...)".
// Évite de faire 1 UPDATE par clé (hot row contention + N round-trips).
// Fix P4.
func (r *pgAuthRepo) BatchIncrementAPIKeyUsage(ctx context.Context, increments map[uuid.UUID]int) error {
	if len(increments) == 0 {
		return nil
	}

	// Construit les placeholders + args : (?::uuid, ?), (?::uuid, ?), ...
	placeholders := make([]string, 0, len(increments))
	args := make([]interface{}, 0, len(increments)*2)
	for keyID, delta := range increments {
		if delta <= 0 {
			continue
		}
		placeholders = append(placeholders, "(?::uuid, ?::int)")
		args = append(args, keyID.String(), delta)
	}
	if len(placeholders) == 0 {
		return nil
	}

	sql := `
		UPDATE api_keys AS k
		SET    usage_count  = k.usage_count + v.delta,
		       last_used_at = NOW()
		FROM   (VALUES ` + strings.Join(placeholders, ",") + `) AS v(id, delta)
		WHERE  k.id = v.id
	`
	return r.db.WithContext(ctx).Exec(sql, args...).Error
}

func (r *pgAuthRepo) FindRefreshTokenByJTI(ctx context.Context, jti uuid.UUID) (*domain.UserSession, error) {
	var session domain.UserSession

	err := r.db.WithContext(ctx).
		Where("id = ? AND is_blocked = ?", jti, false).
		Where("expires_at > ?", time.Now()).
		First(&session).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func (r *pgAuthRepo) DeleteRefreshTokenByJTI(ctx context.Context, jti uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.UserSession{}).
		Where("id = ?", jti).
		Update("is_blocked", true).Error
}

func (r *pgAuthRepo) IsTokenRevoked(ctx context.Context, tokenHash string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.RevokedToken{}).
		Where("token_hash = ? AND expires_at > NOW()", tokenHash).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *pgAuthRepo) RevokeToken(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	token := domain.RevokedToken{
		ID:        uuid.New(),
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	return r.db.WithContext(ctx).Create(&token).Error
}

func (r *pgAuthRepo) FindByKeycloakUserID(ctx context.Context, kcUserID string) (*domain.User, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("keycloak_user_id = ?", strings.TrimSpace(kcUserID)).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

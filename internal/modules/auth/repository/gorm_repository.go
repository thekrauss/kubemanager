package repository

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"gorm.io/gorm"
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

func (r *pgAuthRepo) CheckProjectPermission(ctx context.Context, userID uuid.UUID, projectID uuid.UUID, permSlug string) (bool, error) {
	var count int64

	err := r.db.WithContext(ctx).
		Table("project_members").
		Joins("JOIN roles ON roles.id = project_members.role_id").
		Joins("JOIN role_permissions ON role_permissions.role_id = roles.id").
		Joins("JOIN permissions ON permissions.id = role_permissions.permission_id").
		Where("project_members.user_id = ?", userID).
		Where("project_members.project_id = ?", projectID).
		Where("permissions.slug = ?", permSlug).
		Count(&count).Error

	if err != nil {
		return false, err
	}

	return count > 0, nil
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

func (r *pgAuthRepo) UpdateAPIKeyUsage(ctx context.Context, keyID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&domain.APIKey{}).
		Where("id = ?", keyID).
		Update("last_used_at", &now).Error
}

func (r *pgAuthRepo) SeedDefaultRoles(ctx context.Context) error {
	roleSpecs := map[string][]string{
		domain.RoleViewer: {
			domain.PermProjectView,
			domain.PermLogsView,
		},
		domain.RoleDeveloper: {
			domain.PermProjectView,
			domain.PermProjectEdit,
			domain.PermWorkloadCreate,
			domain.PermLogsView,
		},
		domain.RoleOwner: {
			domain.PermProjectView,
			domain.PermProjectEdit,
			domain.PermProjectDelete,
			domain.PermWorkloadCreate,
			domain.PermWorkloadDelete,
			domain.PermLogsView,
			domain.PermShellExec,
		},
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for roleName, permSlugs := range roleSpecs {
			var permissions []domain.Permission
			for _, slug := range permSlugs {
				var perm domain.Permission

				if err := tx.Where(domain.Permission{Slug: slug}).
					Attrs(domain.Permission{ID: uuid.New()}).
					FirstOrCreate(&perm).Error; err != nil {
					return err
				}
				permissions = append(permissions, perm)
			}

			var role domain.Role

			if err := tx.Where(domain.Role{Name: roleName}).
				Attrs(domain.Role{ID: uuid.New()}).
				FirstOrCreate(&role).Error; err != nil {
				return err
			}

			if err := tx.Model(&role).Association("Permissions").Replace(permissions); err != nil {
				return err
			}

			log.Printf("Seeded Role: %s with %d permissions", roleName, len(permissions))
		}
		return nil
	})
}

func (r *pgAuthRepo) GetRoleByName(ctx context.Context, name string) (*domain.Role, error) {
	var role domain.Role
	err := r.db.WithContext(ctx).
		Preload("Permissions").
		Where("name = ?", name).
		First(&role).Error

	if err != nil {
		return nil, err
	}
	return &role, nil
}

func (r *pgAuthRepo) AddProjectMember(ctx context.Context, member *domain.ProjectMember) error {
	return r.db.WithContext(ctx).Save(member).Error
}

func (r *pgAuthRepo) RemoveProjectMember(ctx context.Context, projectID uuid.UUID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("project_id = ? AND user_id = ?", projectID, userID).
		Delete(&domain.ProjectMember{}).Error
}

func (r *pgAuthRepo) GetProjectMember(ctx context.Context, projectID uuid.UUID, userID uuid.UUID) (*domain.ProjectMember, error) {
	var member domain.ProjectMember

	err := r.db.WithContext(ctx).
		Preload("Role.Permissions").
		Preload("User").
		Where("project_id = ? AND user_id = ?", projectID, userID).
		First(&member).Error

	if err != nil {
		return nil, err
	}
	return &member, nil
}

func (r *pgAuthRepo) GetUserProjectMemberships(ctx context.Context, userID uuid.UUID) ([]domain.ProjectMember, error) {
	var memberships []domain.ProjectMember
	err := r.db.WithContext(ctx).
		Preload("Role").
		Where("user_id = ?", userID).
		Find(&memberships).Error
	return memberships, err
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

func (r *pgAuthRepo) UpdatePassword(ctx context.Context, userID uuid.UUID, hash string) error {

	result := r.db.WithContext(ctx).
		Model(&domain.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"password_hash": hash,
			"updated_at":    time.Now(),
		})

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return errors.New("user not found")
	}

	return nil
}

func (r *pgAuthRepo) ListRoles(ctx context.Context) ([]domain.Role, error) {
	var roles []domain.Role
	err := r.db.WithContext(ctx).Preload("Permissions").Find(&roles).Error
	return roles, err
}

func (r *pgAuthRepo) ListProjectMembers(ctx context.Context, projectID uuid.UUID) ([]domain.ProjectMember, error) {
	var members []domain.ProjectMember
	err := r.db.WithContext(ctx).
		Preload("User").
		Preload("Role").
		Where("project_id = ?", projectID).
		Find(&members).Error
	return members, err
}

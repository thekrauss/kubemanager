package repository

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

type ProjectRepository interface {
	CreateProject(ctx context.Context, project *dauth.Project, ownerID string) error
	DeleteProject(ctx context.Context, projectID string) error
	GetProjectByID(ctx context.Context, id string) (*dauth.Project, error)
}

type pgProjectRepo struct {
	db *gorm.DB
}

func NewProjectRepository(db *gorm.DB) ProjectRepository {
	return &pgProjectRepo{db: db}
}

func (r *pgProjectRepo) CreateProject(ctx context.Context, project *dauth.Project, ownerID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(project).Error; err != nil {
			return err
		}

		var role dauth.Role
		if err := tx.Where("name = ?", dauth.RoleOwner).First(&role).Error; err != nil {
			return err
		}

		member := &dauth.ProjectMember{
			ProjectID: project.ID,
			UserID:    uuid.MustParse(ownerID),
			RoleID:    role.ID,
		}
		return tx.Create(member).Error
	})
}

func (r *pgProjectRepo) DeleteProject(ctx context.Context, projectID string) error {
	return r.db.WithContext(ctx).Unscoped().Where("id = ?", projectID).Delete(&domain.Project{}).Error
}

func (r *pgProjectRepo) GetProjectByID(ctx context.Context, id string) (*dauth.Project, error) {
	var project dauth.Project

	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		Preload("Members").
		First(&project).Error

	if err != nil {
		return nil, err
	}
	return &project, nil
}

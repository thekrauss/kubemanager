package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/organizations"
)

type ProjectRepository interface {
	CreateProject(ctx context.Context, project *dauth.Project, ownerID string) error
	DeleteProject(ctx context.Context, projectID string) error
	GetProjectByID(ctx context.Context, id string) (*dauth.Project, error)
	// UpdateProjectStatus : statut de provisionnement (PENDING, READY, ERROR).
	UpdateProjectStatus(ctx context.Context, id, status string) error
}

type pgProjectRepo struct {
	db *gorm.DB
}

func NewProjectRepository(db *gorm.DB) ProjectRepository {
	return &pgProjectRepo{db: db}
}

// CreateProject crée le projet dans l'organisation personnelle du
// propriétaire (créée au besoin) et lui accorde roles/owner, atomiquement.
func (r *pgProjectRepo) CreateProject(ctx context.Context, project *dauth.Project, ownerID string) error {
	ownerUUID, err := uuid.Parse(ownerID)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if project.OrganizationID == uuid.Nil {
			orgID, err := organizations.EnsurePersonal(ctx, tx, ownerUUID, "")
			if err != nil {
				return err
			}
			project.OrganizationID = orgID
		}
		if err := tx.Create(project).Error; err != nil {
			return err
		}
		if err := iam.AddBinding(ctx, tx, iam.Binding{
			Resource:  iam.ProjectName(project.ID),
			Role:      "roles/owner",
			Principal: iam.UserPrincipal(ownerUUID),
		}, "user:"+ownerID); err != nil {
			return err
		}
		return nil
	})
}

// DeleteProject : la ligne et tous les droits IAM portés par le projet et
// ses ressources (sinon des bindings orphelins laissent croire à leurs
// membres qu'ils ont encore ce projet).
func (r *pgProjectRepo) DeleteProject(ctx context.Context, projectID string) error {
	id, err := uuid.Parse(projectID)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("id = ?", id).Delete(&domain.Project{}).Error; err != nil {
			return err
		}
		return iam.DeleteResourcePolicy(ctx, tx, iam.ProjectName(id))
	})
}

func (r *pgProjectRepo) GetProjectByID(ctx context.Context, id string) (*dauth.Project, error) {
	var project dauth.Project

	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&project).Error

	if err != nil {
		return nil, err
	}
	return &project, nil
}

func (r *pgProjectRepo) UpdateProjectStatus(ctx context.Context, id, status string) error {
	return r.db.WithContext(ctx).Model(&dauth.Project{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now()}).Error
}

// Package repository : persistance des builds.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/modules/builds/domain"
)

// listLimit : builds renvoyés par une liste.
const listLimit = 50

// BuildRepository : accès aux builds.
type BuildRepository interface {
	Create(ctx context.Context, b *domain.Build) error
	Get(ctx context.Context, project, id uuid.UUID) (*domain.Build, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Build, error)
	List(ctx context.Context, project uuid.UUID, name string) ([]domain.Build, error)
	CountActive(ctx context.Context, project uuid.UUID) (int64, error)
	MarkBuilding(ctx context.Context, id uuid.UUID) error
	Finish(ctx context.Context, id uuid.UUID, status, failure, digest, detected string) error
	// ActiveForWorkload : builds en cours destinés à ce conteneur.
	ActiveForWorkload(ctx context.Context, workload uuid.UUID) ([]domain.Build, error)
	// HasNewerForWorkload : un build plus récent vise ce conteneur.
	HasNewerForWorkload(ctx context.Context, workload uuid.UUID, than time.Time) (bool, error)
	// LatestForWorkload : dernier build destiné à ce conteneur (nil : aucun).
	LatestForWorkload(ctx context.Context, workload uuid.UUID) (*domain.Build, error)
	// LatestDeployed : dernier build dont l'image a été envoyée sur ce conteneur.
	LatestDeployed(ctx context.Context, workload uuid.UUID) (*domain.Build, error)
	SetDeployState(ctx context.Context, id uuid.UUID, state string) error
}

type gormRepository struct{ db *gorm.DB }

// New : dépôt GORM.
func New(db *gorm.DB) BuildRepository { return &gormRepository{db: db} }

func (r *gormRepository) Create(ctx context.Context, b *domain.Build) error {
	return r.db.WithContext(ctx).Create(b).Error
}

func (r *gormRepository) Get(ctx context.Context, project, id uuid.UUID) (*domain.Build, error) {
	var b domain.Build
	err := r.db.WithContext(ctx).Where("project_id = ? AND id = ?", project, id).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrBuildNotFound
	}
	return &b, err
}

func (r *gormRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Build, error) {
	var b domain.Build
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrBuildNotFound
	}
	return &b, err
}

func (r *gormRepository) List(ctx context.Context, project uuid.UUID, name string) ([]domain.Build, error) {
	q := r.db.WithContext(ctx).Where("project_id = ?", project)
	if name != "" {
		q = q.Where("name = ?", name)
	}
	out := []domain.Build{}
	return out, q.Order("created_at DESC").Limit(listLimit).Find(&out).Error
}

func (r *gormRepository) CountActive(ctx context.Context, project uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.Build{}).
		Where("project_id = ? AND status IN ?", project, []string{domain.StatusQueued, domain.StatusBuilding}).Count(&n).Error
	return n, err
}

func (r *gormRepository) MarkBuilding(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&domain.Build{}).Where("id = ? AND status = ?", id, domain.StatusQueued).
		Updates(map[string]any{"status": domain.StatusBuilding, "started_at": time.Now()}).Error
}

// Finish : état final ; sans effet sur un build déjà terminé (réessai).
func (r *gormRepository) Finish(ctx context.Context, id uuid.UUID, status, failure, digest, detected string) error {
	return r.db.WithContext(ctx).Model(&domain.Build{}).
		Where("id = ? AND status IN ?", id, []string{domain.StatusQueued, domain.StatusBuilding}).
		Updates(map[string]any{"status": status, "failure": failure, "digest": digest, "detected": detected, "finished_at": time.Now()}).Error
}

func (r *gormRepository) ActiveForWorkload(ctx context.Context, workload uuid.UUID) ([]domain.Build, error) {
	out := []domain.Build{}
	return out, r.db.WithContext(ctx).
		Where("deploy_workload_id = ? AND status IN ?", workload, []string{domain.StatusQueued, domain.StatusBuilding}).
		Find(&out).Error
}

func (r *gormRepository) HasNewerForWorkload(ctx context.Context, workload uuid.UUID, than time.Time) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.Build{}).
		Where("deploy_workload_id = ? AND created_at > ?", workload, than).Count(&n).Error
	return n > 0, err
}

func (r *gormRepository) latest(ctx context.Context, q *gorm.DB) (*domain.Build, error) {
	var b domain.Build
	err := q.WithContext(ctx).Order("created_at DESC").First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &b, err
}

func (r *gormRepository) LatestForWorkload(ctx context.Context, workload uuid.UUID) (*domain.Build, error) {
	return r.latest(ctx, r.db.Where("deploy_workload_id = ?", workload))
}

func (r *gormRepository) LatestDeployed(ctx context.Context, workload uuid.UUID) (*domain.Build, error) {
	return r.latest(ctx, r.db.Where("deploy_workload_id = ? AND deploy_state = ?", workload, domain.DeployStarted))
}

func (r *gormRepository) SetDeployState(ctx context.Context, id uuid.UUID, state string) error {
	return r.db.WithContext(ctx).Model(&domain.Build{}).Where("id = ?", id).Update("deploy_state", state).Error
}

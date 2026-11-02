// Package repository : persistance de la connexion GitHub.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/thekrauss/kubemanager/internal/modules/github/domain"
)

// deliveryRetention : au-delà, GitHub ne rejoue plus une livraison.
const deliveryRetention = 30 * 24 * time.Hour

// Repository : accès aux installations, liens et livraisons.
type Repository interface {
	UpsertInstallation(ctx context.Context, in *domain.Installation) error
	ListInstallations(ctx context.Context, project uuid.UUID) ([]domain.Installation, error)
	GetInstallation(ctx context.Context, project uuid.UUID, installationID int64) (*domain.Installation, error)
	// DeleteInstallation : délie l'installation du projet et supprime les liens
	// de ses conteneurs (les conteneurs continuent de tourner).
	DeleteInstallation(ctx context.Context, project uuid.UUID, installationID int64) error

	GetLink(ctx context.Context, workload uuid.UUID) (*domain.Link, error)
	SaveLink(ctx context.Context, link *domain.Link) error
	DeleteLink(ctx context.Context, workload uuid.UUID) error
	// LinksForPush : liens actifs qui suivent cette branche de ce dépôt, dans un
	// projet où l'installation est toujours liée.
	LinksForPush(ctx context.Context, installationID, repoID int64, branch string) ([]domain.Link, error)
	RenameRepo(ctx context.Context, repoID int64, fullName string) error

	// InstallationGone : installation supprimée chez GitHub.
	InstallationGone(ctx context.Context, installationID int64) error
	SetSuspended(ctx context.Context, installationID int64, suspended bool) error
	DisableRepos(ctx context.Context, installationID int64, repoIDs []int64) error
	EnableRepos(ctx context.Context, installationID int64, repoIDs []int64) error

	// ClaimDelivery : false si cette livraison a déjà été reçue.
	ClaimDelivery(ctx context.Context, deliveryID, event string) (bool, error)
	ReleaseDelivery(ctx context.Context, deliveryID string) error
}

type gormRepository struct{ db *gorm.DB }

// New : dépôt GORM.
func New(db *gorm.DB) Repository { return &gormRepository{db: db} }

func (r *gormRepository) UpsertInstallation(ctx context.Context, in *domain.Installation) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}, {Name: "installation_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"account_login", "account_type", "suspended"}),
	}).Create(in).Error
}

func (r *gormRepository) ListInstallations(ctx context.Context, project uuid.UUID) ([]domain.Installation, error) {
	out := []domain.Installation{}
	return out, r.db.WithContext(ctx).Where("project_id = ?", project).Order("created_at").Find(&out).Error
}

func (r *gormRepository) GetInstallation(ctx context.Context, project uuid.UUID, installationID int64) (*domain.Installation, error) {
	var in domain.Installation
	err := r.db.WithContext(ctx).Where("project_id = ? AND installation_id = ?", project, installationID).First(&in).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrInstallationNotLinked
	}
	return &in, err
}

func (r *gormRepository) DeleteInstallation(ctx context.Context, project uuid.UUID, installationID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("project_id = ? AND installation_id = ?", project, installationID).Delete(&domain.Link{}).Error; err != nil {
			return err
		}
		res := tx.Where("project_id = ? AND installation_id = ?", project, installationID).Delete(&domain.Installation{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.ErrInstallationNotLinked
		}
		return nil
	})
}

func (r *gormRepository) GetLink(ctx context.Context, workload uuid.UUID) (*domain.Link, error) {
	var l domain.Link
	err := r.db.WithContext(ctx).Where("workload_id = ?", workload).First(&l).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrLinkNotFound
	}
	return &l, err
}

func (r *gormRepository) SaveLink(ctx context.Context, link *domain.Link) error {
	return r.db.WithContext(ctx).Save(link).Error
}

func (r *gormRepository) DeleteLink(ctx context.Context, workload uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("workload_id = ?", workload).Delete(&domain.Link{})
	if res.Error == nil && res.RowsAffected == 0 {
		return domain.ErrLinkNotFound
	}
	return res.Error
}

func (r *gormRepository) LinksForPush(ctx context.Context, installationID, repoID int64, branch string) ([]domain.Link, error) {
	out := []domain.Link{}
	return out, r.db.WithContext(ctx).
		Where("installation_id = ? AND repo_id = ? AND branch = ? AND auto_deploy AND disabled_reason = ''", installationID, repoID, branch).
		Where("EXISTS (SELECT 1 FROM github_installations i WHERE i.project_id = workload_git_links.project_id AND i.installation_id = workload_git_links.installation_id AND NOT i.suspended)").
		Find(&out).Error
}

func (r *gormRepository) RenameRepo(ctx context.Context, repoID int64, fullName string) error {
	return r.db.WithContext(ctx).Model(&domain.Link{}).
		Where("repo_id = ? AND repo_full_name <> ?", repoID, fullName).Update("repo_full_name", fullName).Error
}

func (r *gormRepository) InstallationGone(ctx context.Context, installationID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Link{}).Where("installation_id = ?", installationID).
			Update("disabled_reason", domain.DisabledInstallationDeleted).Error; err != nil {
			return err
		}
		return tx.Where("installation_id = ?", installationID).Delete(&domain.Installation{}).Error
	})
}

func (r *gormRepository) SetSuspended(ctx context.Context, installationID int64, suspended bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Installation{}).Where("installation_id = ?", installationID).
			Update("suspended", suspended).Error; err != nil {
			return err
		}
		links := tx.Model(&domain.Link{}).Where("installation_id = ?", installationID)
		if suspended {
			// Un dépôt déjà retiré garde sa cause, plus précise.
			return links.Where("disabled_reason = ''").Update("disabled_reason", domain.DisabledInstallationSuspended).Error
		}
		return links.Where("disabled_reason = ?", domain.DisabledInstallationSuspended).Update("disabled_reason", "").Error
	})
}

func (r *gormRepository) DisableRepos(ctx context.Context, installationID int64, repoIDs []int64) error {
	if len(repoIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&domain.Link{}).Where("installation_id = ? AND repo_id IN ?", installationID, repoIDs).
		Update("disabled_reason", domain.DisabledRepositoryRemoved).Error
}

func (r *gormRepository) EnableRepos(ctx context.Context, installationID int64, repoIDs []int64) error {
	if len(repoIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&domain.Link{}).
		Where("installation_id = ? AND repo_id IN ? AND disabled_reason = ?", installationID, repoIDs, domain.DisabledRepositoryRemoved).
		Update("disabled_reason", "").Error
}

func (r *gormRepository) ClaimDelivery(ctx context.Context, deliveryID, event string) (bool, error) {
	db := r.db.WithContext(ctx)
	if err := db.Where("received_at < ?", time.Now().Add(-deliveryRetention)).Delete(&domain.Delivery{}).Error; err != nil {
		return false, err
	}
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&domain.Delivery{DeliveryID: deliveryID, Event: event, ReceivedAt: time.Now()})
	return res.RowsAffected == 1, res.Error
}

func (r *gormRepository) ReleaseDelivery(ctx context.Context, deliveryID string) error {
	return r.db.WithContext(ctx).Where("delivery_id = ?", deliveryID).Delete(&domain.Delivery{}).Error
}

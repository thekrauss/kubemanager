package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
)

type SiteRepository interface {
	// Sites — toute lecture est scopée par projet (isolation tenant).
	CreateSite(ctx context.Context, site *domain.Site) error
	GetSite(ctx context.Context, projectID, siteID uuid.UUID) (*domain.Site, error)
	GetSiteByID(ctx context.Context, siteID uuid.UUID) (*domain.Site, error)
	ListSites(ctx context.Context, projectID uuid.UUID) ([]domain.Site, error)
	UpdateSite(ctx context.Context, site *domain.Site) error
	SetSiteStatus(ctx context.Context, siteID uuid.UUID, status string) error
	DeleteSite(ctx context.Context, siteID uuid.UUID) error

	// Déploiements
	CreateDeployment(ctx context.Context, d *domain.SiteDeployment) error
	GetDeployment(ctx context.Context, siteID, deploymentID uuid.UUID) (*domain.SiteDeployment, error)
	ListDeployments(ctx context.Context, siteID uuid.UUID, limit int) ([]domain.SiteDeployment, error)
	UpdateDeploymentStatus(ctx context.Context, deploymentID uuid.UUID, status, errMsg string) error
	// MarkDeploymentReady enregistre le résultat de l'extraction et active le
	// déploiement sur le site, dans une seule transaction.
	MarkDeploymentReady(ctx context.Context, siteID, deploymentID uuid.UUID, fileCount int, sizeBytes int64) error
	ActivateDeployment(ctx context.Context, siteID, deploymentID uuid.UUID) error

	// Domaines personnalisés
	CreateDomain(ctx context.Context, d *domain.SiteDomain) error
	GetDomain(ctx context.Context, siteID, domainID uuid.UUID) (*domain.SiteDomain, error)
	ListDomains(ctx context.Context, siteID uuid.UUID) ([]domain.SiteDomain, error)
	SetDomainVerification(ctx context.Context, domainID uuid.UUID, verified bool, at time.Time) error
	// TouchDomain : date de la dernière vérification, rien d'autre.
	TouchDomain(ctx context.Context, domainID uuid.UUID, at time.Time) error
	// VerifiedDomain : domaine vérifié portant ce nom, hors du domaine donné.
	VerifiedDomain(ctx context.Context, hostname string, except uuid.UUID) (*domain.SiteDomain, error)
	DeleteDomain(ctx context.Context, siteID, domainID uuid.UUID) error
}

type siteRepository struct {
	db *gorm.DB
}

func NewSiteRepository(db *gorm.DB) SiteRepository {
	return &siteRepository{db: db}
}

func (r *siteRepository) CreateSite(ctx context.Context, site *domain.Site) error {
	return r.db.WithContext(ctx).Create(site).Error
}

func (r *siteRepository) GetSite(ctx context.Context, projectID, siteID uuid.UUID) (*domain.Site, error) {
	var site domain.Site
	err := r.db.WithContext(ctx).First(&site, "id = ? AND project_id = ?", siteID, projectID).Error
	if err != nil {
		return nil, err
	}
	return &site, nil
}

func (r *siteRepository) GetSiteByID(ctx context.Context, siteID uuid.UUID) (*domain.Site, error) {
	var site domain.Site
	if err := r.db.WithContext(ctx).First(&site, "id = ?", siteID).Error; err != nil {
		return nil, err
	}
	return &site, nil
}

func (r *siteRepository) ListSites(ctx context.Context, projectID uuid.UUID) ([]domain.Site, error) {
	var sites []domain.Site
	err := r.db.WithContext(ctx).Where("project_id = ?", projectID).Order("created_at DESC").Find(&sites).Error
	return sites, err
}

// UpdateSite : réglages du site, seuls modifiables ici. Ne réécrit ni le statut
// ni le déploiement actif : Save écrasait une suppression ou un déploiement
// concurrents avec la copie lue avant.
func (r *siteRepository) UpdateSite(ctx context.Context, site *domain.Site) error {
	return r.db.WithContext(ctx).Model(&domain.Site{}).Where("id = ?", site.ID).
		Updates(map[string]any{"spa_fallback": site.SPAFallback, "updated_at": site.UpdatedAt}).Error
}

func (r *siteRepository) SetSiteStatus(ctx context.Context, siteID uuid.UUID, status string) error {
	return r.db.WithContext(ctx).Model(&domain.Site{}).Where("id = ?", siteID).
		Updates(map[string]any{"status": status, "updated_at": time.Now()}).Error
}

func (r *siteRepository) DeleteSite(ctx context.Context, siteID uuid.UUID) error {
	// FK ON DELETE CASCADE : déploiements et domaines suivent.
	return r.db.WithContext(ctx).Delete(&domain.Site{}, "id = ?", siteID).Error
}

func (r *siteRepository) CreateDeployment(ctx context.Context, d *domain.SiteDeployment) error {
	return r.db.WithContext(ctx).Create(d).Error
}

func (r *siteRepository) GetDeployment(ctx context.Context, siteID, deploymentID uuid.UUID) (*domain.SiteDeployment, error) {
	var d domain.SiteDeployment
	err := r.db.WithContext(ctx).First(&d, "id = ? AND site_id = ?", deploymentID, siteID).Error
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *siteRepository) ListDeployments(ctx context.Context, siteID uuid.UUID, limit int) ([]domain.SiteDeployment, error) {
	var ds []domain.SiteDeployment
	q := r.db.WithContext(ctx).Where("site_id = ?", siteID).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return ds, q.Find(&ds).Error
}

func (r *siteRepository) UpdateDeploymentStatus(ctx context.Context, deploymentID uuid.UUID, status, errMsg string) error {
	return r.db.WithContext(ctx).Model(&domain.SiteDeployment{}).Where("id = ?", deploymentID).
		Updates(map[string]any{"status": status, "error": errMsg, "updated_at": time.Now()}).Error
}

func (r *siteRepository) MarkDeploymentReady(ctx context.Context, siteID, deploymentID uuid.UUID, fileCount int, sizeBytes int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Model(&domain.SiteDeployment{}).Where("id = ? AND site_id = ?", deploymentID, siteID).
			Updates(map[string]any{
				"status": domain.DeploymentStatusReady, "error": "",
				"file_count": fileCount, "size_bytes": sizeBytes, "updated_at": now,
			}).Error; err != nil {
			return err
		}
		return activate(tx, siteID, deploymentID, now)
	})
}

func (r *siteRepository) ActivateDeployment(ctx context.Context, siteID, deploymentID uuid.UUID) error {
	return activate(r.db.WithContext(ctx), siteID, deploymentID, time.Now())
}

// activate ne bascule pas un site en cours de suppression.
func activate(tx *gorm.DB, siteID, deploymentID uuid.UUID, now time.Time) error {
	return tx.Model(&domain.Site{}).
		Where("id = ? AND status <> ?", siteID, domain.SiteStatusDeleting).
		Updates(map[string]any{
			"active_deployment_id": deploymentID,
			"status":               domain.SiteStatusLive,
			"updated_at":           now,
		}).Error
}

func (r *siteRepository) CreateDomain(ctx context.Context, d *domain.SiteDomain) error {
	return r.db.WithContext(ctx).Create(d).Error
}

func (r *siteRepository) GetDomain(ctx context.Context, siteID, domainID uuid.UUID) (*domain.SiteDomain, error) {
	var d domain.SiteDomain
	if err := r.db.WithContext(ctx).First(&d, "id = ? AND site_id = ?", domainID, siteID).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *siteRepository) ListDomains(ctx context.Context, siteID uuid.UUID) ([]domain.SiteDomain, error) {
	var ds []domain.SiteDomain
	return ds, r.db.WithContext(ctx).Where("site_id = ?", siteID).Order("created_at").Find(&ds).Error
}

func (r *siteRepository) TouchDomain(ctx context.Context, domainID uuid.UUID, at time.Time) error {
	return r.db.WithContext(ctx).Model(&domain.SiteDomain{}).Where("id = ?", domainID).Update("checked_at", at).Error
}

func (r *siteRepository) VerifiedDomain(ctx context.Context, hostname string, except uuid.UUID) (*domain.SiteDomain, error) {
	var d domain.SiteDomain
	err := r.db.WithContext(ctx).Where("hostname = ? AND verified AND id <> ?", hostname, except).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &d, err
}

func (r *siteRepository) SetDomainVerification(ctx context.Context, domainID uuid.UUID, verified bool, at time.Time) error {
	updates := map[string]any{"verified": verified, "checked_at": at}
	if verified {
		updates["verified_at"] = at
	}
	return r.db.WithContext(ctx).Model(&domain.SiteDomain{}).Where("id = ?", domainID).Updates(updates).Error
}

func (r *siteRepository) DeleteDomain(ctx context.Context, siteID, domainID uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.SiteDomain{}, "id = ? AND site_id = ?", domainID, siteID).Error
}

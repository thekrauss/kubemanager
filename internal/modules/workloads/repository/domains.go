package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/core/pgerr"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
)

// DomainStore : domaines personnalisés des conteneurs.
type DomainStore interface {
	Create(ctx context.Context, d *domain.WorkloadDomain) error
	List(ctx context.Context, workload uuid.UUID) ([]domain.WorkloadDomain, error)
	Get(ctx context.Context, workload, id uuid.UUID) (*domain.WorkloadDomain, error)
	Delete(ctx context.Context, workload, id uuid.UUID) error
	// VerifiedHosts : noms à router pour ce conteneur.
	VerifiedHosts(ctx context.Context, workload uuid.UUID) ([]string, error)
	// VerifiedOwner : domaine vérifié portant ce nom, hors du domaine donné.
	VerifiedOwner(ctx context.Context, hostname string, except uuid.UUID) (*domain.WorkloadDomain, error)
	// HostnameTaken : nom déjà réservé par un site statique vérifié.
	SiteHostnameTaken(ctx context.Context, hostname string) (bool, error)
	// SetVerified : ErrDomainClaimed si le nom a été pris entre-temps.
	SetVerified(ctx context.Context, id uuid.UUID, verified bool) error
	Touch(ctx context.Context, id uuid.UUID) error
}

type gormDomainStore struct{ db *gorm.DB }

// NewDomainStore : stockage en base.
func NewDomainStore(db *gorm.DB) DomainStore { return &gormDomainStore{db: db} }

func (s *gormDomainStore) Create(ctx context.Context, d *domain.WorkloadDomain) error {
	err := s.db.WithContext(ctx).Create(d).Error
	if isUniqueViolation(err) {
		return domain.ErrDomainExists
	}
	return err
}

func (s *gormDomainStore) List(ctx context.Context, workload uuid.UUID) ([]domain.WorkloadDomain, error) {
	out := []domain.WorkloadDomain{}
	return out, s.db.WithContext(ctx).Where("workload_id = ?", workload).Order("hostname").Find(&out).Error
}

func (s *gormDomainStore) Get(ctx context.Context, workload, id uuid.UUID) (*domain.WorkloadDomain, error) {
	var d domain.WorkloadDomain
	err := s.db.WithContext(ctx).Where("workload_id = ? AND id = ?", workload, id).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrDomainNotFound
	}
	return &d, err
}

func (s *gormDomainStore) Delete(ctx context.Context, workload, id uuid.UUID) error {
	return s.db.WithContext(ctx).Where("workload_id = ? AND id = ?", workload, id).Delete(&domain.WorkloadDomain{}).Error
}

func (s *gormDomainStore) VerifiedHosts(ctx context.Context, workload uuid.UUID) ([]string, error) {
	var out []string
	err := s.db.WithContext(ctx).Model(&domain.WorkloadDomain{}).Where("workload_id = ? AND verified", workload).Order("hostname").Pluck("hostname", &out).Error
	return out, err
}

func (s *gormDomainStore) VerifiedOwner(ctx context.Context, hostname string, except uuid.UUID) (*domain.WorkloadDomain, error) {
	var d domain.WorkloadDomain
	err := s.db.WithContext(ctx).Where("hostname = ? AND verified AND id <> ?", hostname, except).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &d, err
}

func (s *gormDomainStore) SiteHostnameTaken(ctx context.Context, hostname string) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Table("site_domains").Where("hostname = ? AND verified", hostname).Count(&n).Error
	return n > 0, err
}

func (s *gormDomainStore) SetVerified(ctx context.Context, id uuid.UUID, verified bool) error {
	now := time.Now()
	updates := map[string]any{"verified": verified, "checked_at": now, "verified_at": nil}
	if verified {
		updates["verified_at"] = now
	}
	err := s.db.WithContext(ctx).Model(&domain.WorkloadDomain{}).Where("id = ?", id).Updates(updates).Error
	if isUniqueViolation(err) {
		return domain.ErrDomainClaimed
	}
	return err
}

func (s *gormDomainStore) Touch(ctx context.Context, id uuid.UUID) error {
	return s.db.WithContext(ctx).Model(&domain.WorkloadDomain{}).Where("id = ?", id).Update("checked_at", time.Now()).Error
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerr.UniqueViolation
}

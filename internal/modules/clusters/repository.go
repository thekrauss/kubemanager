package clusters

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClusterRepository : persistance du registre des clusters gérés.
type ClusterRepository interface {
	// Upsert : crée ou met à jour un cluster. Set `LastSeenAt = now` côté upsert
	// pour tracker la dernière activité. `EnrolledAt` n'est set qu'au INSERT.
	Upsert(ctx context.Context, clusterID, version string) (*Cluster, error)

	// GetByClusterID : lookup par identifiant logique. ErrRecordNotFound si absent.
	GetByClusterID(ctx context.Context, clusterID string) (*Cluster, error)

	// List : liste tous les clusters (admin view).
	List(ctx context.Context) ([]Cluster, error)

	// TouchLastSeen : update `LastSeenAt = now` sans modifier le reste.
	// Appelé par onPodEvent — moins lourd qu'un Upsert complet.
	TouchLastSeen(ctx context.Context, clusterID string) error
}

type gormClusterRepo struct {
	db *gorm.DB
}

func NewClusterRepository(db *gorm.DB) ClusterRepository {
	return &gormClusterRepo{db: db}
}

// Upsert : ON CONFLICT (cluster_id) DO UPDATE version, last_seen_at, updated_at.
// EnrolledAt préservé sur l'UPDATE (set uniquement au premier INSERT).
func (r *gormClusterRepo) Upsert(ctx context.Context, clusterID, version string) (*Cluster, error) {
	now := time.Now().UTC()
	cluster := &Cluster{
		ClusterID:  clusterID,
		Version:    version,
		LastSeenAt: &now,
		EnrolledAt: now, // ignoré sur UPDATE (DoUpdates cible uniquement version/last_seen_at/updated_at)
	}
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "cluster_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"version":      version,
				"last_seen_at": now,
				"updated_at":   now,
			}),
		}).
		Create(cluster).Error
	if err != nil {
		return nil, err
	}
	// Re-SELECT pour récupérer l'EnrolledAt persisté (celui de l'INSERT initial).
	return r.GetByClusterID(ctx, clusterID)
}

func (r *gormClusterRepo) GetByClusterID(ctx context.Context, clusterID string) (*Cluster, error) {
	var c Cluster
	err := r.db.WithContext(ctx).
		Where("cluster_id = ?", clusterID).
		First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *gormClusterRepo) List(ctx context.Context) ([]Cluster, error) {
	var out []Cluster
	err := r.db.WithContext(ctx).Order("created_at DESC").Find(&out).Error
	return out, err
}

func (r *gormClusterRepo) TouchLastSeen(ctx context.Context, clusterID string) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&Cluster{}).
		Where("cluster_id = ?", clusterID).
		Updates(map[string]interface{}{
			"last_seen_at": now,
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("cluster not found — Enroll missing?")
	}
	return nil
}

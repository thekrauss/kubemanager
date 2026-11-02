package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thekrauss/kubemanager/internal/core/pgerr"

	"github.com/google/uuid"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/api/resource"
)

type WorkloadRepository interface {
	Create(ctx context.Context, workload *domain.Workload) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Workload, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Workload, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, phase string) error
	// UpdateSpec : nouvelle version (image, disque, URL publique, spec déclarative).
	UpdateSpec(ctx context.Context, id uuid.UUID, image, storageSize, externalURL string, spec []byte) error
	// UpdateRuntime : réplicas, limites, port et TLS (comptés dans les quotas).
	UpdateRuntime(ctx context.Context, id uuid.UUID, replicas int, cpuLimit, memoryLimit string, targetPort int, tls bool) error
	Delete(ctx context.Context, id uuid.UUID) error
	// DeleteByProject hard-deletes every workload row belonging to a project.
	// Used by the delete-project saga after the K8s namespace is torn down.
	DeleteByProject(ctx context.Context, projectID uuid.UUID) (int64, error)
	GetTotalUsageByProject(ctx context.Context, projectID uuid.UUID) (totalCPU int64, totalMem int64, totalStorage int64, err error)
}

type workloadRepository struct {
	db *gorm.DB
}

func NewWorkloadRepository(db *gorm.DB) WorkloadRepository {
	return &workloadRepository{db: db}
}

// ErrNameTaken : un conteneur du projet porte déjà ce nom (index unique).
var ErrNameTaken = errors.New("a container with this name already exists in the project")

func (r *workloadRepository) Create(ctx context.Context, workload *domain.Workload) error {
	err := r.db.WithContext(ctx).Create(workload).Error
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerr.UniqueViolation {
		return ErrNameTaken // deux créations simultanées du même nom
	}
	return err
}

func (r *workloadRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Workload, error) {
	var workload domain.Workload
	err := r.db.WithContext(ctx).First(&workload, "id = ?", id).Error
	return &workload, err
}

func (r *workloadRepository) ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Workload, error) {
	var workloads []domain.Workload
	err := r.db.WithContext(ctx).Where("project_id = ?", projectID).Find(&workloads).Error
	return workloads, err
}

func (r *workloadRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, phase string) error {
	return r.db.WithContext(ctx).Model(&domain.Workload{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":        status,
			"current_phase": phase,
		}).Error
}

func (r *workloadRepository) UpdateRuntime(ctx context.Context, id uuid.UUID, replicas int, cpuLimit, memoryLimit string, targetPort int, tls bool) error {
	return r.db.WithContext(ctx).Model(&domain.Workload{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"replicas":     replicas,
			"cpu_limit":    cpuLimit,
			"memory_limit": memoryLimit,
			"target_port":  targetPort,
			"tls_enabled":  tls,
		}).Error
}

func (r *workloadRepository) UpdateSpec(ctx context.Context, id uuid.UUID, image, storageSize, externalURL string, spec []byte) error {
	return r.db.WithContext(ctx).Model(&domain.Workload{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"image":         image,
			"storage_size":  storageSize,
			"external_url":  externalURL,
			"spec":          spec,
			"status":        utils.WorkloadStarting,
			"current_phase": utils.PhaseHelmChartLoading,
		}).Error
}

func (r *workloadRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Workload{}, "id = ?", id).Error
}

func (r *workloadRepository) DeleteByProject(ctx context.Context, projectID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).Where("project_id = ?", projectID).Delete(&domain.Workload{})
	return res.RowsAffected, res.Error
}

func (r *workloadRepository) GetTotalUsageByProject(ctx context.Context, projectID uuid.UUID) (totalCPU int64, totalMem int64, totalStorage int64, err error) {
	var workloads []domain.Workload

	err = r.db.WithContext(ctx).
		Where("project_id = ? AND status NOT IN ?", projectID, []string{utils.WorkloadFailed, utils.WorkloadDeleted}).
		Find(&workloads).Error
	if err != nil {
		return 0, 0, 0, err
	}

	for _, w := range workloads {
		replicas := int64(w.Replicas)
		if replicas <= 0 {
			replicas = 1
		}

		totalCPU += r.parseCPUToMilli(w.CPULimit) * replicas
		totalMem += r.parseMemToMi(w.MemoryLimit) * replicas

		if w.PersistenceEnabled {
			totalStorage += r.parseSizeToMi(w.StorageSize)
		}
	}

	return totalCPU, totalMem, totalStorage, nil
}

// "200m" en 200 ou "1" en 1000
func (r *workloadRepository) parseCPUToMilli(cpu string) int64 {
	q, err := resource.ParseQuantity(cpu)
	if err != nil {
		return 0
	}
	return q.MilliValue()
}

// "256Mi" en 256 ou "1Gi" en 1024
func (r *workloadRepository) parseMemToMi(mem string) int64 {
	q, err := resource.ParseQuantity(mem)
	if err != nil {
		return 0
	}
	return q.Value() / utils.BytesPerMi
}

// parseSizeToMi : même unité que les quotas du service (en Gi, le disque déjà
// utilisé comptait 1024 fois moins que le disque demandé).
func (r *workloadRepository) parseSizeToMi(size string) int64 {
	return r.parseMemToMi(size)
}

package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/api/resource"
)

type WorkloadRepository interface {
	Create(ctx context.Context, workload *domain.Workload) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Workload, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Workload, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, phase string) error
	Delete(ctx context.Context, id uuid.UUID) error
	GetTotalUsageByProject(ctx context.Context, projectID uuid.UUID) (totalCPU int64, totalMem int64, totalStorage int64, err error)
}

type workloadRepository struct {
	db *gorm.DB
}

func NewWorkloadRepository(db *gorm.DB) WorkloadRepository {
	return &workloadRepository{db: db}
}

func (r *workloadRepository) Create(ctx context.Context, workload *domain.Workload) error {
	return r.db.WithContext(ctx).Create(workload).Error
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

func (r *workloadRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Workload{}, "id = ?", id).Error
}

func (r *workloadRepository) GetTotalUsageByProject(ctx context.Context, projectID uuid.UUID) (totalCPU int64, totalMem int64, totalStorage int64, err error) {
	var workloads []domain.Workload

	err = r.db.WithContext(ctx).
		Where("project_id = ? AND status NOT IN ('FAILED', 'DELETED')", projectID).
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
			totalStorage += r.parseSizeToGi(w.StorageSize)
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
	return q.Value() / (1024 * 1024)
}

func (r *workloadRepository) parseSizeToGi(size string) int64 {
	q, err := resource.ParseQuantity(size)
	if err != nil {
		return 0
	}
	return q.Value() / (1024 * 1024 * 1024)
}

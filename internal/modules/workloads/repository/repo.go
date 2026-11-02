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
	// UpdateStatus, UpdateRuntime, UpdateSpec : domain.ErrWorkloadDeleting si le
	// conteneur est en suppression, sauf pour les statuts de la suppression.
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

// deletingStatuses : plus rien ne doit réécrire ces conteneurs, sauf la
// suppression elle-même : un déploiement ou un événement de pod tardif masquerait
// la suppression, et un nouveau déploiement réinstallerait le conteneur.
var deletingStatuses = []string{utils.WorkloadDeleting, utils.WorkloadDeleted}

// updateLive : écriture conditionnelle. Aucune ligne touchée : le conteneur est
// en suppression ou n'existe plus (domain.ErrWorkloadDeleting).
func (r *workloadRepository) updateLive(ctx context.Context, id uuid.UUID, values map[string]any) error {
	res := r.db.WithContext(ctx).Model(&domain.Workload{}).
		Where("id = ? AND status NOT IN ?", id, deletingStatuses).
		Updates(values)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.ErrWorkloadDeleting
	}
	return nil
}

func (r *workloadRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, phase string) error {
	values := map[string]any{"status": status, "current_phase": phase}
	switch status {
	case utils.WorkloadDeleting, utils.WorkloadDeleted, utils.WorkloadDeleteFailed:
		return r.db.WithContext(ctx).Model(&domain.Workload{}).Where("id = ?", id).Updates(values).Error
	}
	return r.updateLive(ctx, id, values)
}

func (r *workloadRepository) UpdateRuntime(ctx context.Context, id uuid.UUID, replicas int, cpuLimit, memoryLimit string, targetPort int, tls bool) error {
	return r.updateLive(ctx, id, map[string]any{
		"replicas":     replicas,
		"cpu_limit":    cpuLimit,
		"memory_limit": memoryLimit,
		"target_port":  targetPort,
		"tls_enabled":  tls,
	})
}

// UpdateSpec ne touche pas au statut : le workflow est déjà lancé, et il peut
// avoir écrit son résultat.
func (r *workloadRepository) UpdateSpec(ctx context.Context, id uuid.UUID, image, storageSize, externalURL string, spec []byte) error {
	return r.updateLive(ctx, id, map[string]any{
		"image":        image,
		"storage_size": storageSize,
		"external_url": externalURL,
		"spec":         spec,
	})
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

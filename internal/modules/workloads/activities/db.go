package activities

import (
	"context"

	"github.com/google/uuid"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type WorkloadDBActivities struct {
	DB     *gorm.DB
	Logger *zap.SugaredLogger
	Repo   repository.WorkloadRepository
}

func (a *WorkloadDBActivities) UpdateWorkloadStatus(ctx context.Context, workloadID string, status string, phase string) error {
	a.Logger.Infow("Updating workload status in DB", "id", workloadID, "status", status, "phase", phase)

	uID, _ := uuid.Parse(workloadID)
	return a.Repo.UpdateStatus(ctx, uID, status, phase)
}

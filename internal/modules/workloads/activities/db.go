package activities

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/infrastructure/observability"
	"github.com/thekrauss/kubemanager/internal/infrastructure/realtime"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
)

// WorkloadDBActivities : activities DB du module workload.
//
// Publisher est optionnel — si nil, les transitions ne sont pas broadcastées
// (pattern "fail-open" : un Centrifugo down ne doit JAMAIS bloquer un deploy).
type WorkloadDBActivities struct {
	DB        *gorm.DB
	Logger    *zap.SugaredLogger
	Repo      repository.WorkloadRepository
	Publisher realtime.Publisher // optionnel : noop si Centrifugo disabled
}

// DeploymentEvent : payload publié sur `project:X:workload:Y:deploy`.
// Shape stable contractuelle avec le frontend — NE PAS modifier sans versioning.
type DeploymentEvent struct {
	WorkloadID string    `json:"workload_id"`
	ProjectID  string    `json:"project_id"`
	Status     string    `json:"status"` // STARTING / RUNNING / FAILED / DELETING ...
	Phase      string    `json:"phase"`  // HELM_PREPARING / HELM_INSTALLING / DEPLOYED ...
	At         time.Time `json:"at"`
}

// UpdateWorkloadStatus : transition de statut/phase + broadcast Centrifugo.
//
// Pattern "DB first, broadcast second" :
//  1. Persist en DB (source de vérité — si ça fail, on abort)
//  2. Publish Centrifugo (best-effort — si ça fail, on log et on continue)
//
// Le projectID est nécessaire pour construire le channel. Le workflow le
// connaît via DeployWorkloadInput/DeleteWorkloadInput.ProjectID.
func (a *WorkloadDBActivities) UpdateWorkloadStatus(ctx context.Context, workloadID, projectID, status, phase string) error {
	a.Logger.Infow("Updating workload status", "id", workloadID, "status", status, "phase", phase)

	uID, err := uuid.Parse(workloadID)
	if err != nil {
		return err
	}
	if err := a.Repo.UpdateStatus(ctx, uID, status, phase); err != nil {
		if errors.Is(err, domain.ErrWorkloadDeleting) {
			// Définitif : le workflow s'arrête sans rien installer.
			return temporal.NewNonRetryableApplicationError(err.Error(), errTypeWorkloadDeleting, err)
		}
		return err
	}

	// Business metric : toute transition alimente la PrometheusRule
	// `WorkloadSagaDeleteFailures` (status=DELETE_FAILED).
	observability.WorkloadStatusTransitions.WithLabelValues(status, phase).Inc()

	// Best-effort broadcast — ne PAS remonter l'erreur : un Centrifugo down
	// ne doit pas bloquer le workflow Temporal.
	a.broadcastDeploymentEvent(ctx, workloadID, projectID, status, phase)
	return nil
}

// SoftDeleteWorkload : marque le workload comme supprimé en DB + broadcast final.
// Appelé en fin de DeleteWorkloadWorkflow après uninstall Helm réussie.
func (a *WorkloadDBActivities) SoftDeleteWorkload(ctx context.Context, workloadID, projectID string) error {
	a.Logger.Infow("Soft-deleting workload", "id", workloadID)
	uID, err := uuid.Parse(workloadID)
	if err != nil {
		return err
	}
	if err := a.Repo.Delete(ctx, uID); err != nil {
		return err
	}

	// Dernier event : workload disparu. Le frontend peut clore le subscribe.
	a.broadcastDeploymentEvent(ctx, workloadID, projectID, utils.WorkloadDeleted, utils.PhaseRemoved)
	return nil
}

// broadcastDeploymentEvent : publie sur le channel workload + le channel projet
// (pour que les UIs listant "tous les workloads du projet" reçoivent aussi).
//
// Volontairement silencieux sur erreur : Centrifugo est un canal d'UX live,
// pas un journal d'audit — une défaillance ici ne doit pas remonter au user.
func (a *WorkloadDBActivities) broadcastDeploymentEvent(ctx context.Context, workloadID, projectID, status, phase string) {
	if a.Publisher == nil || projectID == "" {
		return
	}
	event := DeploymentEvent{
		WorkloadID: workloadID,
		ProjectID:  projectID,
		Status:     status,
		Phase:      phase,
		At:         time.Now().UTC(),
	}

	// Channel spécifique au workload (granularité fine pour l'UI détail).
	if err := a.Publisher.Publish(ctx, realtime.DeploymentChannel(projectID, workloadID), event); err != nil {
		a.Logger.Warnw("centrifugo publish deployment failed", "workload", workloadID, "error", err)
	}
	// Channel projet (pour listes/dashboards qui veulent voir tous les workloads).
	if err := a.Publisher.Publish(ctx, realtime.ProjectChannel(projectID), event); err != nil {
		a.Logger.Warnw("centrifugo publish project failed", "project", projectID, "error", err)
	}
}

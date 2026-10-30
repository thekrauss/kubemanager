package clusters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/infrastructure/observability"
	"github.com/thekrauss/kubemanager/internal/infrastructure/realtime"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	workloadRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
)

// IClusterService : contrat du service, mockable pour tests.
type IClusterService interface {
	Enroll(ctx context.Context, req EnrollRequest) (*EnrollResponse, error)
	GetConfig(ctx context.Context, clusterID string) (*RemoteConfigResponse, error)
	OnPodEvent(ctx context.Context, clusterID string, event PodEvent) (*PodEventAck, error)
	ListClusters(ctx context.Context) ([]Cluster, error)
}

type service struct {
	workloads workloadRepo.WorkloadRepository
	clusters  ClusterRepository
	publisher realtime.Publisher
	logger    *zap.SugaredLogger
}

// Defaults RemoteConfig — valeurs saines pour un watcher single-cluster.
// Peuvent être surchargées par cluster via une table config dédiée.
var defaultRemoteConfig = RemoteConfigResponse{
	DedupCooldownSeconds: 60,
	BatchSize:            50,
	BatchFlushMs:         1000,
	EnabledReasons: []string{
		"CrashLoopBackOff",
		"ImagePullBackOff",
		"CreateContainerConfigError",
		"OOMKilled",
		"Failed",
		"Evicted",
	},
}

func NewService(
	workloads workloadRepo.WorkloadRepository,
	clusters ClusterRepository,
	publisher realtime.Publisher,
	logger *zap.SugaredLogger,
) IClusterService {
	return &service{
		workloads: workloads,
		clusters:  clusters,
		publisher: publisher,
		logger:    logger.With("service", "ClusterService"),
	}
}

// ── Enroll ────────────────────────────────────────────────────────────────

// Enroll : Upsert DB. Si le cluster existait déjà → "updated", sinon "enrolled".
// Distinction basée sur `EnrolledAt == CreatedAt` (proxy pour "première fois").
func (s *service) Enroll(ctx context.Context, req EnrollRequest) (*EnrollResponse, error) {
	clusterID := strings.TrimSpace(req.ClusterID)
	if clusterID == "" {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "cluster_id is required")
	}

	// Check si existant AVANT upsert pour distinguer enrolled / updated.
	status := enrollStatusEnrolled
	if _, err := s.clusters.GetByClusterID(ctx, clusterID); err == nil {
		status = enrollStatusUpdated
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("cluster lookup: %w", err)
	}

	if _, err := s.clusters.Upsert(ctx, clusterID, req.Version); err != nil {
		return nil, fmt.Errorf("cluster upsert: %w", err)
	}

	// Business metric — gauge 1 par cluster enrollé, nourrit le dashboard
	// "fleet size over time".
	observability.ClusterEnrolled.WithLabelValues(clusterID).Set(1)

	s.logger.Infow("cluster enrolled", "cluster_id", clusterID, "version", req.Version, "status", status)
	return &EnrollResponse{
		Status:    status,
		ClusterID: clusterID,
		At:        time.Now().UTC(),
	}, nil
}

// ListClusters : admin view — tous les clusters connus, avec leur dernière activité.
func (s *service) ListClusters(ctx context.Context) ([]Cluster, error) {
	return s.clusters.List(ctx)
}

// ── GetConfig ─────────────────────────────────────────────────────────────

func (s *service) GetConfig(ctx context.Context, clusterID string) (*RemoteConfigResponse, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "cluster_id is required")
	}
	// Pour l'instant, defaults partagés. Évolution : table clusters_config.
	cfg := defaultRemoteConfig
	return &cfg, nil
}

// ── OnPodEvent ────────────────────────────────────────────────────────────

// Mapping K8s reason → status workload interne.
// Exhaustif sur les reasons courants ; default = garde le status existant.
var reasonToWorkloadStatus = map[string]string{
	reasonCrashLoopBackOff:           utils.WorkloadCrashLoop,
	reasonImagePullBackOff:           utils.WorkloadImagePullFailed,
	reasonCreateContainerConfigError: utils.WorkloadConfigError,
	reasonOOMKilled:                  utils.WorkloadOOMKilled,
	reasonFailed:                     utils.WorkloadFailed,
	reasonEvicted:                    utils.WorkloadEvicted,
}

// OnPodEvent : traite un event pod reçu du watcher.
//
// Flow :
//  1. Extraire workloadID depuis event.Labels["km-workload-id"] (posé par notre chart)
//  2. Vérifier que le workload existe en DB
//  3. Map reason → status + update DB (UpdateWorkloadStatus = broadcast Centrifugo inclus)
//  4. Publier AUSSI un event brut sur le channel pour que l'UI affiche les détails pod
//
// Si labels absents ou workload inconnu → ACK "ignored" (pas d'erreur — le watcher
// peut voir des pods hors scope, ex: system pods).
func (s *service) OnPodEvent(ctx context.Context, clusterID string, event PodEvent) (*PodEventAck, error) {
	// Best-effort : touch la dernière activité du cluster (monitoring staleness).
	// Si le cluster n'a jamais Enroll, on log mais on continue — pour ne pas
	// bloquer le premier event d'un watcher qui aurait raté son Enroll.
	if err := s.clusters.TouchLastSeen(ctx, clusterID); err != nil {
		s.logger.Debugw("touch cluster last_seen failed", "cluster", clusterID, "error", err)
	}

	// Business metric — gauge alimente l'alerte `ClusterWatcherStale`.
	observability.ClusterLastSeenTimestamp.WithLabelValues(clusterID).SetToCurrentTime()

	workloadIDStr := extractWorkloadID(event)
	if workloadIDStr == "" {
		s.logger.Debugw("pod event without km-workload-id — ignored",
			"cluster", clusterID, "pod", event.Name, "namespace", event.Namespace)
		return &PodEventAck{EventID: event.EventID, Status: ackStatusIgnored}, nil
	}

	workloadID, err := uuid.Parse(workloadIDStr)
	if err != nil {
		s.logger.Warnw("pod event with invalid workload_id label",
			"cluster", clusterID, "pod", event.Name, "label", workloadIDStr)
		return &PodEventAck{EventID: event.EventID, Status: ackStatusIgnored}, nil
	}

	wl, err := s.workloads.GetByID(ctx, workloadID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Workload supprimé côté DB mais pod pas encore cleanup côté K8s.
			return &PodEventAck{EventID: event.EventID, Status: ackStatusIgnored}, nil
		}
		return nil, fmt.Errorf("workload lookup: %w", err)
	}

	newStatus, mapped := reasonToWorkloadStatus[event.Reason]
	if mapped {
		phase := utils.PhasePodEventPrefix + event.Reason
		if err := s.workloads.UpdateStatus(ctx, workloadID, newStatus, phase); err != nil {
			s.logger.Errorw("update workload status failed", "workload", workloadID, "error", err)
			// On continue quand même le broadcast — l'UI doit voir l'event même si DB down.
		}
	}

	// Broadcast Centrifugo : event brut ET status update condensé.
	s.publishPodEvent(ctx, wl.ProjectID.String(), workloadID.String(), event, mapped, newStatus)

	s.logger.Infow("pod event routed",
		"cluster", clusterID,
		"workload", workloadID,
		"pod", event.Name,
		"reason", event.Reason,
		"status", newStatus,
	)

	ack := &PodEventAck{EventID: event.EventID, WorkloadID: workloadID.String()}
	if mapped {
		ack.Status = ackStatusMapped
	} else {
		ack.Status = ackStatusAccepted
	}
	observability.PodEventsIngested.WithLabelValues(clusterID, ack.Status).Inc()
	return ack, nil
}

// publishPodEvent : push l'event sur le channel deploy du workload.
// Shape cohérent avec DeploymentEvent (cf workload/activities/db.go).
func (s *service) publishPodEvent(ctx context.Context, projectID, workloadID string, event PodEvent, mapped bool, newStatus string) {
	if s.publisher == nil || projectID == "" {
		return
	}
	payload := map[string]any{
		"type":          podEventType,
		"workload_id":   workloadID,
		"project_id":    projectID,
		"cluster_event": event, // raw watcher event pour debug/UI détaillée
		"mapped_status": newStatus,
		"mapped":        mapped,
		"at":            time.Now().UTC(),
	}
	ch := realtime.DeploymentChannel(projectID, workloadID)
	if err := s.publisher.Publish(ctx, ch, payload); err != nil {
		s.logger.Warnw("centrifugo publish pod event failed", "channel", ch, "error", err)
	}
}

// extractWorkloadID : tente dans l'ordre
//  1. event.Labels["km-workload-id"]       ← posé par notre Helm chart
//  2. event.Labels["workload-id"]          ← fallback watcher (cf processor.go:92)
//  3. event.ProjectID field (si le watcher l'a déjà mappé côté Labels watcher)
func extractWorkloadID(event PodEvent) string {
	if event.Labels != nil {
		if id := strings.TrimSpace(event.Labels[utils.LabelKmWorkloadID]); id != "" {
			return id
		}
		if id := strings.TrimSpace(event.Labels[utils.LabelWatcherWorkloadID]); id != "" {
			return id
		}
	}
	return ""
}

// Assurance compile-time que le service satisfait bien l'interface publique.
var _ IClusterService = (*service)(nil)

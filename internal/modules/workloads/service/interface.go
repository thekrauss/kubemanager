package service

import (
	"context"
	"io"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
)

type IWorkloadService interface {
	//  CRUD
	Deploy(ctx context.Context, req *WorkloadServiceRequest) (*domain.Workload, error)
	Get(ctx context.Context, id uuid.UUID) (*domain.Workload, error)
	List(ctx context.Context, projectID uuid.UUID) ([]domain.Workload, error)
	Update(ctx context.Context, id string, req domain.UpdateWorkloadRequest) error
	ListDomains(ctx context.Context, workload uuid.UUID) ([]domain.DomainResponse, error)
	AddDomain(ctx context.Context, workload uuid.UUID, hostname string) (*domain.DomainResponse, error)
	VerifyDomain(ctx context.Context, workload, id uuid.UUID) (*domain.DomainResponse, error)
	RemoveDomain(ctx context.Context, workload, id uuid.UUID) error
	ListVariables(ctx context.Context, id uuid.UUID) ([]domain.VariableView, error)
	PatchVariables(ctx context.Context, id uuid.UUID, req domain.PatchVariablesRequest) (*domain.VariablesResponse, error)
	Delete(ctx context.Context, id uuid.UUID) error

	//  Lifecycle K8s (scaling / rollout)
	// Stop : scale deployment à 0 replicas. Billing peut s'arrêter.
	Stop(ctx context.Context, id uuid.UUID) error
	// Start : scale deployment au `Replicas` stocké en DB.
	Start(ctx context.Context, id uuid.UUID) error
	// Restart : rollout restart (pod-par-pod) sans changer l'image ni l'état.
	Restart(ctx context.Context, id uuid.UUID) error
	// Rollback : helm rollback à la révision `toRevision` (0 = précédente).
	Rollback(ctx context.Context, id uuid.UUID, toRevision int) error

	//  Observabilité (read-only)
	// LiveStatus : merge de l'état DB + statut runtime (replicas ready, pods phase).
	LiveStatus(ctx context.Context, id uuid.UUID) (*domain.LiveStatus, error)
	// Logs : stream des logs du premier pod prêt (ou erreur si aucun).
	// Le caller DOIT appeler Close() sur le ReadCloser retourné.
	Logs(ctx context.Context, id uuid.UUID, opts LogOptions) (io.ReadCloser, error)
	// Events : liste les K8s Events récents du namespace filtrés sur ce workload.
	Events(ctx context.Context, id uuid.UUID) ([]domain.WorkloadEvent, error)
	// Metrics : consommation actuelle (CPU/RAM) mesurée par le metrics-server
	// pour tous les pods portant le label km-workload-id=<uuid>, + les valeurs
	// réservées stockées en DB (Replicas × CPULimit/MemoryLimit).
	Metrics(ctx context.Context, id uuid.UUID) (*domain.WorkloadMetrics, error)
}

// LogOptions : paramètres de lecture des logs pod.
type LogOptions struct {
	Follow       bool
	Previous     bool // logs du précédent container (pour debug crashloop)
	TailLines    int64
	SinceSeconds int64 // lookback, 0 = tout
	Timestamps   bool
}

// DefaultLogOptions : raisonnable pour un "kubectl logs" équivalent.
func DefaultLogOptions() LogOptions {
	return LogOptions{
		TailLines:    defaultLogTailLines,
		SinceSeconds: int64(defaultLogLookback.Seconds()),
		Timestamps:   true,
	}
}

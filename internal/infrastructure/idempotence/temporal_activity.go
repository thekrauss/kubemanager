// temporal_activity.go — helper d'idempotence pour activities Temporal.
//
// Usage typique dans une activity :
//
//     func (a *Activities) CreateNamespace(ctx context.Context, payload CreateNamespacePayload) (*Result, error) {
//         result, _, err := idempotency.ExecuteActivity[*Result](ctx, a.idempotency, payload,
//             idempotency.ActivityOptions{ProcessingTTL: 2 * time.Minute},
//             func() (*Result, error) {
//                 // … appels K8s réels …
//                 return doCreateNamespace(ctx, payload)
//             },
//         )
//         return result, err
//     }
//
// La clé d'idempotence est dérivée de :
//   - WorkflowID + ActivityID (déduplique les retries Temporal naturellement)
//   - Hash SHA256 du payload (détecte un replay avec payload différent)
package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
)

// ErrActivityInProgress est retournée par ExecuteActivity quand un autre runner
// est en train d'exécuter la même activity (lock Redis toujours détenu).
//
// Important : on RETOURNE cette erreur explicitement (au lieu de nil + status
// WorkerInProgress) pour que Temporal déclenche un retry — sans ça, l'activity
// rapporterait un faux succès et le workflow avancerait sur un état partiel.
var ErrActivityInProgress = errors.New("idempotent activity already in progress — retry")

// ActivityOptions : paramètres pour ExecuteActivity (sous-ensemble de WorkerExecutionOptions).
type ActivityOptions struct {
	ProcessingTTL   time.Duration
	OnFinalizeError func(stage string, err error)
}

// ExecuteActivity exécute `work` de manière idempotente dans une activity Temporal.
//
//   - Au premier appel : exécute `work`, stocke le résultat sous la key (WorkflowID:ActivityID).
//   - Sur retry/replay : retourne le résultat mis en cache (pas de re-exécution).
//   - Si un autre runner est en cours (BeginInProgress) : renvoie WorkerInProgress — l'appelant
//     peut décider de retry ou d'échouer.
//
// `payload` est hashé en SHA256 pour détecter un changement de paramètres entre retries.
func ExecuteActivity[T any](
	ctx context.Context,
	mgr Manager,
	payload any,
	opts ActivityOptions,
	work func() (T, error),
) (T, WorkerExecutionStatus, error) {
	var zero T

	// Bypass si pas de manager (tests, mode dégradé).
	if mgr == nil {
		result, err := work()
		if err != nil {
			return zero, "", err
		}
		return result, WorkerExecuted, nil
	}

	info := activity.GetInfo(ctx)
	key := activityKey(info)

	hash, err := mgr.Hash(payload, info.ActivityType.Name)
	if err != nil {
		// Hash failure → on run quand même pour ne pas bloquer.
		result, wErr := work()
		if wErr != nil {
			return zero, "", wErr
		}
		return result, WorkerExecuted, nil
	}

	result, status, err := ExecuteWorker(ctx, mgr, key, hash, WorkerExecutionOptions{
		ProcessingTTL:   opts.ProcessingTTL,
		OnFinalizeError: opts.OnFinalizeError,
	}, work)
	if err != nil {
		return zero, status, err
	}
	// Fix H7 : si un autre runner détient le lock, on NE DOIT PAS rapporter
	// un succès (Temporal avancerait sur un state partiel). On retourne
	// ErrActivityInProgress → Temporal retry automatiquement.
	if status == WorkerInProgress {
		return zero, status, ErrActivityInProgress
	}
	return result, status, nil
}

// activityKey : une clé par exécution d'activité, partagée par ses tentatives.
// Le RunID en fait partie : un workflow relancé sous le même identifiant
// (ex. workload-update-<id> à chaque nouvelle version) est une nouvelle
// exécution ; sans lui, la clé de la version précédente était retrouvée avec
// un autre contenu (« idempotency payload mismatch ») et la mise à jour
// échouait.
func activityKey(info activity.Info) string {
	return fmt.Sprintf("tmp:%s:%s:%s:%s", info.WorkflowExecution.ID, info.WorkflowExecution.RunID,
		info.ActivityType.Name, info.ActivityID)
}

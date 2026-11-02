// lifecycle.go — opérations de cycle de vie des workloads.
//
// Division claire :
//   - Deploy / Delete passent par Temporal (atomicité + retries sur ops longues)
//   - Stop / Start / Restart / Rollback : appels K8s/Helm directs (ops atomiques)
//   - Get / List / LiveStatus / Logs / Events : read-only, pas de workflow
package service

import (
	"context"
	"fmt"
	"io"
	"time"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/google/uuid"
	"helm.sh/helm/v3/pkg/action"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1 "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/thekrauss/kubemanager/internal/infrastructure/helm"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
)

//  CRUD read-side

func (s *WorkloadService) Get(ctx context.Context, id uuid.UUID) (*domain.Workload, error) {
	return s.Repo.GetByID(ctx, id)
}

func (s *WorkloadService) List(ctx context.Context, projectID uuid.UUID) ([]domain.Workload, error) {
	return s.Repo.ListByProject(ctx, projectID)
}

//  Delete : passe par Temporal (Saga : Helm uninstall + DB soft-delete)

func (s *WorkloadService) Delete(ctx context.Context, id uuid.UUID) error {
	workload, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return &betoerrors.Error{Code: betoerrors.CodeNotFound, Message: "workload not found", Cause: err}
	}

	// Suppression déjà lancée : même résultat, rien à refaire. Sinon (workflow
	// jamais démarré, DELETING resté en base), elle reprend ici.
	deleteID := workflows.WorkflowIDDeletePrefix + id.String()
	running, err := s.Distributor.IsRunning(ctx, deleteID)
	if err != nil {
		return err
	}
	if running {
		return nil
	}

	// Marque immédiatement DELETING : plus aucun déploiement ne peut démarrer
	// (écriture conditionnelle, y compris dans le workflow).
	if err := s.Repo.UpdateStatus(ctx, id, utils.WorkloadDeleting, utils.PhaseHelmUninstalling); err != nil {
		return err
	}

	// DELETING est écrit : la suite ne doit pas dépendre de la requête.
	ctx = context.WithoutCancel(ctx)
	s.stopDeploys(ctx, id)

	_, err = s.Distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: deleteID},
		workflows.DeleteWorkloadWorkflowName,
		DeleteWorkloadInput{
			WorkloadID:  id.String(),
			ProjectID:   workload.ProjectID.String(),
			Namespace:   workload.Namespace,
			ReleaseName: workload.Name,
		},
	)
	if err == nil || temporal.IsAlreadyStarted(err) {
		return nil
	}
	s.compensate(ctx, "workload delete failure not recorded", id, func(ctx context.Context) error {
		return s.Repo.UpdateStatus(ctx, id, utils.WorkloadDeleteFailed, utils.PhaseHelmUninstallErr)
	})
	return err
}

// stopDeploys : un déploiement encore en cours réinstallerait le conteneur après
// sa désinstallation : il resterait dans Kubernetes, hors de la base, et
// consommerait le quota du projet. Annulation plutôt qu'arrêt brutal : l'activité
// Helm reçoit l'annulation et s'interrompt, et le workflow ne se ferme qu'ensuite
// (WaitForCancellation). L'arrêt brutal ne sert que si rien ne répond (worker
// tombé) : le prochain heartbeat d'une activité encore vivante l'interrompra.
func (s *WorkloadService) stopDeploys(ctx context.Context, id uuid.UUID) {
	for _, prefix := range []string{workflows.WorkflowIDDeployPrefix, workflows.WorkflowIDUpdatePrefix} {
		wfID := prefix + id.String()
		running, err := s.Distributor.IsRunning(ctx, wfID)
		if err != nil {
			s.warn("deploy state unavailable before delete", "workflow", wfID, "error", err)
		} else if !running {
			continue
		}
		if err := s.Distributor.CancelWorkflow(ctx, wfID, ""); err != nil {
			s.warn("deploy not cancelled before delete", "workflow", wfID, "error", err)
		}
		if s.waitStopped(ctx, wfID) {
			continue
		}
		s.warn("deploy still running after cancel, terminating", "workflow", wfID)
		if err := s.Distributor.TerminateWorkflow(ctx, wfID, "", "workload deleted"); err != nil {
			s.warn("deploy not terminated before delete", "workflow", wfID, "error", err)
		}
	}
}

// waitStopped : attend la fin du workflow, dans la limite de deployStopWait.
func (s *WorkloadService) waitStopped(ctx context.Context, workflowID string) bool {
	deadline := time.After(deployStopWait)
	ticker := time.NewTicker(deployStopPoll)
	defer ticker.Stop()
	for {
		if running, err := s.Distributor.IsRunning(ctx, workflowID); err == nil && !running {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-ticker.C:
		}
	}
}

// DeleteWorkloadInput : signature partagée entre service et workflow.
// Duplique le type de workflows/ pour éviter un import cycle.
type DeleteWorkloadInput struct {
	WorkloadID  string
	ProjectID   string
	Namespace   string
	ReleaseName string
}

//  Lifecycle K8s direct (Stop/Start/Restart)

// Stop : scale deployment à 0. Le workload reste en DB (billing on-demand).
// Idempotent : scale 0 → 0 est un no-op côté K8s.
func (s *WorkloadService) Stop(ctx context.Context, id uuid.UUID) error {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := ensureNotDeleting(w); err != nil {
		return err
	}
	if err := ensureBuilt(w); err != nil {
		return err
	}
	if err := s.scaleTo(ctx, w, 0); err != nil {
		return fmt.Errorf("scale to 0: %w", err)
	}
	return s.Repo.UpdateStatus(ctx, id, utils.WorkloadStopped, utils.PhaseScaledToZero)
}

// Start : remonte le deployment à `w.Replicas` (valeur DB, pas 1 en dur).
func (s *WorkloadService) Start(ctx context.Context, id uuid.UUID) error {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := ensureNotDeleting(w); err != nil {
		return err
	}
	if err := ensureBuilt(w); err != nil {
		return err
	}
	target := int32(w.Replicas)
	if target <= 0 {
		target = 1
	}
	if err := s.scaleTo(ctx, w, target); err != nil {
		return fmt.Errorf("scale to %d: %w", target, err)
	}
	return s.Repo.UpdateStatus(ctx, id, utils.WorkloadRunning, utils.PhaseStarted)
}

// Restart : rollout restart via patch de l'annotation.
// Pattern standard `kubectl rollout restart deployment`.
func (s *WorkloadService) Restart(ctx context.Context, id uuid.UUID) error {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := ensureNotDeleting(w); err != nil {
		return err
	}
	if err := ensureBuilt(w); err != nil {
		return err
	}
	patch := fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`,
		utils.AnnotationRestartedAt, time.Now().UTC().Format(time.RFC3339),
	)
	_, err = s.K8sClient.AppsV1().Deployments(w.Namespace).Patch(
		ctx, w.Name,
		patchTypeMergePatch, []byte(patch),
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch deployment: %w", err)
	}
	// Redémarrage progressif : les anciens pods servent jusqu'à ce que les
	// nouveaux soient prêts, le conteneur reste RUNNING (avant : RESTARTING,
	// que rien ne faisait jamais repasser à RUNNING).
	return s.Repo.UpdateStatus(ctx, id, utils.WorkloadRunning, utils.PhaseRolloutRestart)
}

// Rollback : helm rollback à la révision cible (0 = revision précédente).
// Utilise le Helm SDK avec le REST config partagé par l'app.
func (s *WorkloadService) Rollback(ctx context.Context, id uuid.UUID, toRevision int) error {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := ensureNotDeleting(w); err != nil {
		return err
	}
	if err := ensureBuilt(w); err != nil {
		return err
	}
	cfg, err := s.newHelmActionConfig(w.Namespace)
	if err != nil {
		return fmt.Errorf("helm config: %w", err)
	}

	rb := action.NewRollback(cfg)
	rb.Version = toRevision // 0 = previous release
	rb.Wait = true
	rb.Timeout = helmRollbackTimeout

	if err := rb.Run(w.Name); err != nil {
		return fmt.Errorf("helm rollback: %w", err)
	}
	return s.Repo.UpdateStatus(ctx, id, utils.WorkloadRunning, utils.PhaseRolledBack)
}

//  Observabilité

func (s *WorkloadService) LiveStatus(ctx context.Context, id uuid.UUID) (*domain.LiveStatus, error) {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	status := &domain.LiveStatus{
		ID:          w.ID.String(),
		Name:        w.Name,
		Namespace:   w.Namespace,
		Status:      w.Status,
		Phase:       w.CurrentPhase,
		ExternalURL: w.ExternalURL,
		Image:       w.Image,
		Replicas:    int32(w.Replicas),
		UpdatedAt:   w.UpdatedAt,
	}

	// Best-effort : si le deployment n'existe pas encore (en cours de déploiement)
	// on retourne juste l'état DB sans la partie K8s.
	dep, err := s.K8sClient.AppsV1().Deployments(w.Namespace).Get(ctx, w.Name, metav1.GetOptions{})
	if err == nil {
		status.ReadyReplicas = dep.Status.ReadyReplicas
		status.AvailableReplicas = dep.Status.AvailableReplicas
	} else if !kerrors.IsNotFound(err) {
		s.Logger.Warnw("deployment lookup failed", "workload", id, "error", err)
	}

	// Liste des pods (labelSelector standard Helm).
	pods, err := s.K8sClient.CoreV1().Pods(w.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: utils.LabelApp + "=" + w.Name,
	})
	if err == nil {
		status.Pods = make([]domain.PodStatus, 0, len(pods.Items))
		for _, p := range pods.Items {
			ps := domain.PodStatus{
				Name:     p.Name,
				Phase:    string(p.Status.Phase),
				NodeName: p.Spec.NodeName,
			}
			if p.Status.StartTime != nil {
				ps.StartedAt = p.Status.StartTime.Time
			}
			for _, c := range p.Status.ContainerStatuses {
				ps.RestartCount += c.RestartCount
				ps.Ready = ps.Ready || c.Ready
			}
			status.Pods = append(status.Pods, ps)
		}
	}

	return status, nil
}

// Logs : stream les logs du premier pod en état Running (ou Pending si aucun Ready).
// Le ReadCloser retourné DOIT être fermé par l'appelant.
func (s *WorkloadService) Logs(ctx context.Context, id uuid.UUID, opts LogOptions) (io.ReadCloser, error) {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	pods, err := s.K8sClient.CoreV1().Pods(w.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: utils.LabelApp + "=" + w.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, betoerrors.New(betoerrors.CodeNotFound, "no pods found for workload")
	}

	// Priorité : premier pod Running, sinon n'importe lequel.
	target := pods.Items[0]
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			target = p
			break
		}
	}

	podOpts := &corev1.PodLogOptions{
		Follow:     opts.Follow,
		Previous:   opts.Previous,
		Timestamps: opts.Timestamps,
	}
	if opts.TailLines > 0 {
		podOpts.TailLines = &opts.TailLines
	}
	if opts.SinceSeconds > 0 {
		podOpts.SinceSeconds = &opts.SinceSeconds
	}

	req := s.K8sClient.CoreV1().Pods(w.Namespace).GetLogs(target.Name, podOpts)
	return req.Stream(ctx)
}

// Events : K8s events récents filtrés par label workload.
// L'ordre est chronologique descendant (plus récent en premier).
func (s *WorkloadService) Events(ctx context.Context, id uuid.UUID) ([]domain.WorkloadEvent, error) {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// K8s Events ne supportent pas labelSelector → on filtre par fieldSelector
	// sur le sub-resource "involvedObject.namespace" puis post-filter par préfixe
	// du nom (ressources créées par Helm ont le release name en préfixe).
	list, err := s.K8sClient.CoreV1().Events(w.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	out := make([]domain.WorkloadEvent, 0, len(list.Items))
	for _, e := range list.Items {
		// Filtre : objets dont le nom commence par le release name.
		if e.InvolvedObject.Name != w.Name && len(e.InvolvedObject.Name) > len(w.Name) {
			prefix := e.InvolvedObject.Name[:len(w.Name)]
			if prefix != w.Name {
				continue
			}
		} else if e.InvolvedObject.Name != w.Name {
			continue
		}
		ev := domain.WorkloadEvent{
			Type:    e.Type,
			Reason:  e.Reason,
			Message: e.Message,
			Object:  fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name),
			Count:   e.Count,
		}
		if !e.FirstTimestamp.IsZero() {
			ev.FirstSeen = e.FirstTimestamp.Time
		}
		if !e.LastTimestamp.IsZero() {
			ev.LastSeen = e.LastTimestamp.Time
		}
		out = append(out, ev)
	}
	return out, nil
}

// Metrics : consommation actuelle CPU/RAM mesurée par le Metrics Server
// pour tous les pods portant le label `km-workload-id=<uuid>`, augmentée des
// valeurs réservées stockées en DB (Replicas × CPULimit/MemoryLimit).
//
// Metrics Server requis dans le cluster cible. Si absent, renvoie uniquement
// les valeurs réservées et CPUUsage/MemoryUsage = "0m"/"0Mi".
func (s *WorkloadService) Metrics(ctx context.Context, id uuid.UUID) (*domain.WorkloadMetrics, error) {
	w, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	out := &domain.WorkloadMetrics{
		WorkloadID:     id.String(),
		CPUUsage:       zeroCPUUsage,
		MemoryUsage:    zeroMemoryUsage,
		ReservedCPU:    w.CPULimit,
		ReservedMemory: w.MemoryLimit,
		Replicas:       w.Replicas,
	}

	mClient, err := metricsv1.NewForConfig(s.K8sConfig)
	if err != nil {
		s.Logger.Warnw("metrics client unavailable, returning reserved only", "error", err)
		return out, nil
	}

	selector := utils.LabelKmWorkloadID + "=" + id.String()
	podMetrics, err := mClient.MetricsV1beta1().PodMetricses(w.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		s.Logger.Warnw("metrics server lookup failed", "workload", id, "error", err)
		return out, nil
	}

	var totalCPUMilli, totalMemBytes int64
	for _, pod := range podMetrics.Items {
		for _, c := range pod.Containers {
			totalCPUMilli += c.Usage.Cpu().MilliValue()
			totalMemBytes += c.Usage.Memory().Value()
		}
	}
	out.PodCount = len(podMetrics.Items)
	out.CPUUsage = fmt.Sprintf("%dm", totalCPUMilli)
	out.MemoryUsage = fmt.Sprintf("%dMi", totalMemBytes/utils.BytesPerMi)
	return out, nil
}

//  Helpers privés

func (s *WorkloadService) scaleTo(ctx context.Context, w *domain.Workload, replicas int32) error {
	scale, err := s.K8sClient.AppsV1().Deployments(w.Namespace).GetScale(ctx, w.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if scale.Spec.Replicas == replicas {
		return nil
	}
	scale.Spec.Replicas = replicas
	_, err = s.K8sClient.AppsV1().Deployments(w.Namespace).UpdateScale(ctx, w.Name, scale, metav1.UpdateOptions{})
	return err
}

func (s *WorkloadService) newHelmActionConfig(namespace string) (*action.Configuration, error) {
	return helm.NewActionConfig(s.K8sConfig, namespace, func(format string, v ...interface{}) {
		s.Logger.Debugf(format, v...)
	})
}

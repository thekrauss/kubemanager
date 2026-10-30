package activities

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsv1 "k8s.io/metrics/pkg/client/clientset/versioned"

	idempotency "github.com/thekrauss/kubemanager/internal/infrastructure/idempotence"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	domain "github.com/thekrauss/kubemanager/internal/modules/projects/domain"
)

// ProjectK8sActivities : activities Temporal manipulant des ressources Kubernetes.
// Le champ Idempotency (optionnel) déduplique les retries via ExecuteActivity.
type ProjectK8sActivities struct {
	K8sClient     *kubernetes.Clientset
	MetricsClient *metricsv1.Clientset
	Logger        *zap.SugaredLogger
	Rbac          authSvc.IRBACService
	Idempotency   idempotency.AtomicManager
}

// CreateNamespace est idempotente : si le namespace existe déjà (AlreadyExists),
// on considère l'opération réussie (classique pour une activity Temporal retriable).
func (a *ProjectK8sActivities) CreateNamespace(ctx context.Context, projectID string, projectName string) error {
	payload := struct {
		ProjectID, ProjectName string
	}{projectID, projectName}

	_, _, err := idempotency.ExecuteActivity(ctx, a.Idempotency, payload,
		idempotency.ActivityOptions{ProcessingTTL: 2 * time.Minute},
		func() (struct{}, error) {
			nsName := fmt.Sprintf("km-%s", projectName)
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubemanager.io/managed":    "true",
						"kubemanager.io/project-id": projectID,
						"kubemanager.io/name":       projectName,
					},
					Annotations: map[string]string{
						"kubemanager.io/created-at": time.Now().Format(time.RFC3339),
					},
				},
			}
			_, err := a.K8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			if err != nil && !kerrors.IsAlreadyExists(err) {
				return struct{}{}, fmt.Errorf("k8s create namespace: %w", err)
			}
			return struct{}{}, nil
		},
	)
	return err
}

func (a *ProjectK8sActivities) DeleteNamespace(ctx context.Context, projectName string) error {
	payload := struct{ ProjectName string }{projectName}

	_, _, err := idempotency.ExecuteActivity(ctx, a.Idempotency, payload,
		idempotency.ActivityOptions{ProcessingTTL: 2 * time.Minute},
		func() (struct{}, error) {
			nsName := fmt.Sprintf("km-%s", projectName)
			a.Logger.Infow("deleting Kubernetes namespace", "namespace", nsName)
			err := a.K8sClient.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
			if err != nil && !kerrors.IsNotFound(err) {
				return struct{}{}, fmt.Errorf("failed to delete namespace %s: %w", nsName, err)
			}
			return struct{}{}, nil
		},
	)
	return err
}

func (a *ProjectK8sActivities) ReconcileProjectResources(ctx context.Context, nsName, cpu, mem string) error {
	payload := struct{ NS, CPU, Mem string }{nsName, cpu, mem}

	_, _, err := idempotency.ExecuteActivity(ctx, a.Idempotency, payload,
		idempotency.ActivityOptions{ProcessingTTL: 2 * time.Minute},
		func() (struct{}, error) {
			if _, err := a.K8sClient.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{}); err != nil {
				return struct{}{}, fmt.Errorf("namespace %s missing, reconciliation failed: %w", nsName, err)
			}

			cpuQ, err := resource.ParseQuantity(cpu)
			if err != nil {
				return struct{}{}, fmt.Errorf("invalid cpu limit %q: %w", cpu, err)
			}
			memQ, err := resource.ParseQuantity(mem)
			if err != nil {
				return struct{}{}, fmt.Errorf("invalid memory limit %q: %w", mem, err)
			}
			quotaName := "project-quota"
			newQuotas := &corev1.ResourceQuota{
				ObjectMeta: metav1.ObjectMeta{Name: quotaName, Namespace: nsName},
				Spec: corev1.ResourceQuotaSpec{
					Hard: corev1.ResourceList{
						corev1.ResourceRequestsCPU:    cpuQ,
						corev1.ResourceRequestsMemory: memQ,
						corev1.ResourceLimitsCPU:      cpuQ,
						corev1.ResourceLimitsMemory:   memQ,
					},
				},
			}

			_, err = a.K8sClient.CoreV1().ResourceQuotas(nsName).Get(ctx, quotaName, metav1.GetOptions{})
			if kerrors.IsNotFound(err) {
				if _, cErr := a.K8sClient.CoreV1().ResourceQuotas(nsName).Create(ctx, newQuotas, metav1.CreateOptions{}); cErr != nil {
					return struct{}{}, fmt.Errorf("create quota: %w", cErr)
				}
				return struct{}{}, nil
			}
			if err != nil {
				return struct{}{}, fmt.Errorf("get quota: %w", err)
			}
			if _, uErr := a.K8sClient.CoreV1().ResourceQuotas(nsName).Update(ctx, newQuotas, metav1.UpdateOptions{}); uErr != nil {
				return struct{}{}, fmt.Errorf("update quota: %w", uErr)
			}
			return struct{}{}, nil
		},
	)
	return err
}

// AssignDefaultOwner : conservée pour les workflows déjà en cours (le nom de
// l'activité fait partie de leur historique Temporal). Le rôle owner est
// désormais accordé dans la même transaction que la création du projet
// (repository.CreateProject), il n'y a plus rien à faire ici.
func (a *ProjectK8sActivities) AssignDefaultOwner(ctx context.Context, projectID, userID string) error {
	a.Logger.Debugw("owner already granted at project creation", "projectID", projectID, "userID", userID)
	return nil
}

func (a *ProjectK8sActivities) GetNamespaceMetrics(ctx context.Context, projectName string) (*domain.NamespaceMetrics, error) {
	nsName := fmt.Sprintf("km-%s", projectName)

	podMetricsList, err := a.MetricsClient.MetricsV1beta1().PodMetricses(nsName).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for ns %s: %w", nsName, err)
	}

	var totalCPU, totalMem int64
	for _, pod := range podMetricsList.Items {
		for _, container := range pod.Containers {
			totalCPU += container.Usage.Cpu().MilliValue()
			totalMem += container.Usage.Memory().Value()
		}
	}

	return &domain.NamespaceMetrics{
		CPUUsage:    fmt.Sprintf("%dm", totalCPU),
		MemoryUsage: fmt.Sprintf("%dMi", totalMem/1024/1024),
	}, nil
}

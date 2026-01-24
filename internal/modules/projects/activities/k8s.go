package activities

import (
	"context"
	"fmt"
	"time"

	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"

	"github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsv1 "k8s.io/metrics/pkg/client/clientset/versioned"
)

type ProjectK8sActivities struct {
	K8sClient     *kubernetes.Clientset
	MetricsClient *metricsv1.Clientset
	Logger        *zap.SugaredLogger
	Rbac          authSvc.RBACService
}

func (a *ProjectK8sActivities) CreateNamespace(ctx context.Context, projectID string, projectName string) error {
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
	if err != nil {
		return fmt.Errorf("k8s error: %w", err)
	}
	return nil
}

func (a *ProjectK8sActivities) DeleteNamespace(ctx context.Context, projectName string) error {
	nsName := fmt.Sprintf("km-%s", projectName)
	a.Logger.Infow("deleting Kubernetes namespace", "namespace", nsName)

	err := a.K8sClient.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete namespace %s: %w", nsName, err)
	}

	return nil
}

func (a *ProjectK8sActivities) ReconcileProjectResources(ctx context.Context, nsName, cpu, mem string) error {

	_, err := a.K8sClient.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("namespace %s missing, reconciliation failed: %w", nsName, err)
	}

	quotaName := "project-quota"
	newQuotas := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      quotaName,
			Namespace: nsName,
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU:    resource.MustParse(cpu),
				corev1.ResourceRequestsMemory: resource.MustParse(mem),
				corev1.ResourceLimitsCPU:      resource.MustParse(cpu),
				corev1.ResourceLimitsMemory:   resource.MustParse(mem),
			},
		},
	}

	_, err = a.K8sClient.CoreV1().ResourceQuotas(nsName).Get(ctx, quotaName, metav1.GetOptions{})
	if err != nil {
		_, err = a.K8sClient.CoreV1().ResourceQuotas(nsName).Create(ctx, newQuotas, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("k8s error: %w", err)
		}
	} else {
		_, err = a.K8sClient.CoreV1().ResourceQuotas(nsName).Update(ctx, newQuotas, metav1.UpdateOptions{})
	}

	return nil
}

func (a *ProjectK8sActivities) AssignDefaultOwner(ctx context.Context, projectID, userID string) error {
	a.Logger.Infow("Assigning default owner to project", "projectID", projectID, "userID", userID)

	err := a.Rbac.AssignProjectRole(ctx, &authdomain.AssignRoleRequest{
		ProjectID: projectID,
		UserID:    userID,
		RoleName:  authdomain.RoleTypes.Owner.String(),
	})

	if err != nil {
		return fmt.Errorf("rbac assignment failed: %w", err)
	}
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

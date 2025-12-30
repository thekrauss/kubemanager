package activities

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ProjectK8sActivities struct {
	K8sClient *kubernetes.Clientset
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

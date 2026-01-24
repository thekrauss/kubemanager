package activities

import (
	"context"
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type WorkloadActivities struct {
	K8sConfig *rest.Config
	K8sClient *kubernetes.Clientset
}

type InstallWorkloadInput struct {
	ReleaseName        string
	Namespace          string
	ImageRepo          string
	ImageTag           string
	ExternalURL        string
	ServiceType        string //"ClusterIP" ou "LoadBalancer"
	Env                map[string]string
	Secrets            map[string]string
	PersistenceEnabled bool
	StorageSize        string
	StorageClass       string
	Replicas           int
}

func (a *WorkloadActivities) InstallChart(ctx context.Context, input InstallWorkloadInput) error {
	settings := cli.New()

	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), input.Namespace, "secret", func(format string, v ...interface{}) {
		fmt.Printf(format, v...)
	}); err != nil {
		return err
	}

	client := action.NewUpgrade(actionConfig)
	client.Install = true
	client.Namespace = input.Namespace
	client.Wait = true
	client.Timeout = 5 * 60

	chartPath := "/app/internal/infrastructure/helm/charts/standard-app"
	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}
	vals := map[string]interface{}{
		"replicaCount": input.Replicas,
		"image": map[string]interface{}{
			"repository": input.ImageRepo,
			"tag":        input.ImageTag,
		},
		"ingress": map[string]interface{}{
			"host": input.ExternalURL,
		},

		"persistence": map[string]interface{}{
			"enabled":      input.PersistenceEnabled,
			"size":         input.StorageSize,
			"storageClass": input.StorageClass,
		},
		"envSecretName": input.ReleaseName + "-env",
	}

	if len(input.Env) > 0 {
		vals["envVars"] = input.Env
	}

	_, err = client.Run(input.ReleaseName, chart, vals)
	if err != nil {
		return fmt.Errorf("helm release failed: %w", err)
	}
	return nil
}

func (a *WorkloadActivities) EnsureSecret(ctx context.Context, nsNam, releaseName string, data map[string]string) error {
	secretName := releaseName + "-env"
	_, err := a.K8sClient.CoreV1().Secrets(nsNam).Get(ctx, secretName, metav1.GetOptions{})

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: nsNam},
		StringData: data,
	}

	if err != nil {
		_, err = a.K8sClient.CoreV1().Secrets(nsNam).Create(ctx, secret, metav1.CreateOptions{})
	} else {
		_, err = a.K8sClient.CoreV1().Secrets(nsNam).Update(ctx, secret, metav1.UpdateOptions{})
	}
	return err
}

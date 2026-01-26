package kubernetes

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsv1 "k8s.io/metrics/pkg/client/clientset/versioned"
)

type ProviderK8s struct {
	Client        *kubernetes.Clientset
	Config        *rest.Config
	MetricsClient *metricsv1.Clientset
}

func NewKubernetesProvider(kubeConfigPath string) (*ProviderK8s, error) {
	var config *rest.Config
	var err error

	if kubeConfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig from %s: %w", kubeConfigPath, err)
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load in-cluster config: %w", err)
		}
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s clientset: %w", err)
	}

	mClient, err := metricsv1.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics clientset: %w", err)
	}

	return &ProviderK8s{
		Client:        clientset,
		Config:        config,
		MetricsClient: mClient,
	}, nil
}

func (p *ProviderK8s) GetServerVersion() (string, error) {
	version, err := p.Client.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	return version.String(), nil
}

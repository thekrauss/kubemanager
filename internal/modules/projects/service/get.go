package service

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1 "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
)

func (s *ProjectService) GetProjectStatus(ctx context.Context, projectID string) (*domain.ProjectStatusResponse, error) {

	project, err := s.Repos.GetProjectByID(ctx, projectID)
	if err != nil {
		return nil, err
	}

	res := &domain.ProjectStatusResponse{
		ProjectID: project.ID.String(),
		Status:    project.Status,
		CreatedAt: project.CreatedAt,
	}

	switch project.Status {

	case utils.ProjectStatusReady:
		nsName := fmt.Sprintf("km-%s", project.Name)
		ns, err := s.K8sClient.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})

		if err != nil {
			res.Status = utils.ProjectStatusError
			res.Phase = utils.PhaseProvisioningFailed
		} else {
			res.K8sInfo = &domain.NamespaceStatus{
				Name:   ns.Name,
				Exists: true,
				Phase:  string(ns.Status.Phase),
			}
			res.Phase = utils.PhaseProvisioningSuccess
		}

	case utils.ProjectStatusProvisioning:
		res.Phase = utils.PhaseK8sNamespaceLinking

	case utils.ProjectStatusError:
		res.Phase = utils.PhaseProvisioningFailed

	case utils.ProjectStatusDeleting:
		res.Phase = utils.PhaseRollbackInitiated

	default:
		res.Phase = "UNKNOWN_STATE"
	}

	return res, nil
}

type ProjectOverview struct {
	ReservedStorage int64 // Ce qui est en DB (ex: 10Gi)
	CurrentUsage    int64 // Ce qui vient de K8s Metrics (ex: 2Gi)
	QuotaLimit      int64 // La limite max autorisée pour ce projet (ex: 20Gi)
}

func (s *ProjectService) GetMetrics(ctx context.Context, projectID string) (*domain.NamespaceMetrics, error) {
	project, err := s.Repos.GetProjectByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	nsName := fmt.Sprintf("km-%s", project.Name)

	mClient, err := metricsv1.NewForConfig(s.K8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %w", err)
	}

	podMetrics, err := mClient.MetricsV1beta1().PodMetricses(nsName).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("metrics server error: %w", err)
	}

	var totalCPU, totalMem int64
	for _, pod := range podMetrics.Items {
		for _, container := range pod.Containers {
			totalCPU += container.Usage.Cpu().MilliValue()
			totalMem += container.Usage.Memory().Value()
		}
	}

	resCPU, resMem, resStorage, _ := s.WorkloadRepo.GetTotalUsageByProject(ctx, project.ID)

	return &domain.NamespaceMetrics{
		CPUUsage:    fmt.Sprintf("%dm", totalCPU),
		MemoryUsage: fmt.Sprintf("%dMi", totalMem/1024/1024),
		ReservedUsage: domain.UsageDTO{
			CPU:     fmt.Sprintf("%dm", resCPU),
			Memory:  fmt.Sprintf("%dMi", resMem),
			Storage: fmt.Sprintf("%dGi", resStorage),
		},
	}, nil
}

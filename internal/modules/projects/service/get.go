package service

import (
	"context"
	"fmt"

	"github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

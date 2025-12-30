package workflows

import (
	"context"
	"fmt"
	"time"

	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"

	"go.temporal.io/sdk/workflow"
)

type ProjectActivities struct{}

func (a *ProjectActivities) CreateProjectInDB(ctx context.Context, name string, description string, ownerID string) (*dauth.Project, error) {
	return nil, nil
}
func (a *ProjectActivities) CreateNamespace(ctx context.Context, projectID string, projectName string) error {
	return nil
}

func (a *ProjectActivities) DeleteProjectDBActivity(ctx context.Context, projectID string) error {
	return nil
}

type CreateProjectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	OwnerID string `json:"owner_id"`

	CpuLimit    string `json:"cpu_limit"`
	MemoryLimit string `json:"memory_limit"`
}

type ProjectResult struct {
	ProjectID string `json:"project_id"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

func CreateProjectWorkflow(ctx workflow.Context, input CreateProjectInput) (ProjectResult, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	var a *ProjectActivities
	var dbRes dauth.Project
	var result ProjectResult

	err := workflow.ExecuteLocalActivity(ctx, a.CreateProjectInDB, input.Name, input.Description, input.OwnerID).Get(ctx, &dbRes)
	if err != nil {
		return result, err
	}

	result.ProjectID = dbRes.ID.String()

	err = workflow.ExecuteActivity(ctx, a.CreateNamespace, result.ProjectID, input.Name).Get(ctx, nil)
	if err != nil {
		compensateOptions := workflow.ActivityOptions{StartToCloseTimeout: 1 * time.Minute}
		ctxComp := workflow.WithActivityOptions(ctx, compensateOptions)

		_ = workflow.ExecuteLocalActivity(ctxComp, a.DeleteProjectDBActivity, result.ProjectID).Get(ctx, nil)
		return result, fmt.Errorf("failed to provision k8s namespace, rolled back: %w", err)
	}

	result.Status = utils.ProjectStatusReady
	result.Namespace = fmt.Sprintf("km-%s", input.Name)
	return result, nil

}

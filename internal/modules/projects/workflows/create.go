package workflows

import (
	"context"
	"fmt"
	"time"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"

	"go.temporal.io/sdk/temporal"
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

func (a *ProjectActivities) ReconcileProjectResources(ctx context.Context, nsName, cpu, mem string) error {
	return nil
}

func (a *ProjectActivities) AssignProjectRole(ctx context.Context, userID, projectID, roleName string) error {
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
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: time.Second,
			MaximumAttempts: 3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	localOptions := workflow.LocalActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
	}
	ctx = workflow.WithLocalActivityOptions(ctx, localOptions)

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
		compensateOptions := workflow.LocalActivityOptions{StartToCloseTimeout: 1 * time.Minute}
		ctxComp := workflow.WithLocalActivityOptions(ctx, compensateOptions)

		_ = workflow.ExecuteLocalActivity(ctxComp, a.DeleteProjectDBActivity, result.ProjectID).Get(ctx, nil)
		return result, fmt.Errorf("failed to provision k8s namespace, rolled back: %w", err)
	}
	err = workflow.ExecuteActivity(ctx, a.ReconcileProjectResources, result.Namespace, input.CpuLimit, input.MemoryLimit).Get(ctx, nil)
	if err != nil {
		return result, err
	}

	result.Status = utils.ProjectStatusReady
	result.Namespace = fmt.Sprintf("km-%s", input.Name)

	rbacInput := &domain.AssignRoleRequest{
		UserID:    input.OwnerID,
		ProjectID: result.ProjectID,
		RoleName:  domain.RoleTypes.Owner.String(),
	}

	err = workflow.ExecuteActivity(ctx, a.AssignProjectRole, rbacInput).Get(ctx, nil)
	if err != nil {
		return result, err
	}
	return result, nil
}



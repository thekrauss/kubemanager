package workflows

import (
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
)

type DeleteWorkloadInput struct {
	WorkloadID  string
	ProjectID   string
	Namespace   string
	ReleaseName string
}

func DeleteWorkloadWorkflow(ctx workflow.Context, input DeleteWorkloadInput) error {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: activityStartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: activityRetryInitialInterval,
			MaximumAttempts: activityRetryMaximumAttempts,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	var dbActs *activities.WorkloadDBActivities
	var helmActs *activities.WorkloadActivities

	if err := workflow.ExecuteActivity(ctx, helmActs.UninstallChart,
		input.Namespace, input.ReleaseName).Get(ctx, nil); err != nil {
		_ = workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
			input.WorkloadID, input.ProjectID, utils.WorkloadDeleteFailed, utils.PhaseHelmUninstallErr).Get(ctx, nil)
		return err
	}

	if err := workflow.ExecuteActivity(ctx, dbActs.SoftDeleteWorkload,
		input.WorkloadID, input.ProjectID).Get(ctx, nil); err != nil {
		_ = workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
			input.WorkloadID, input.ProjectID, utils.WorkloadDeleteFailed, utils.PhaseDBDeleteError).Get(ctx, nil)
		return err
	}

	return nil
}

package workflows

import (
	"time"

	"github.com/thekrauss/kubemanager/internal/modules/projects/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func DeleteProjectWorkflow(ctx workflow.Context, projectID, projectName string) error {
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

	var k8sActs *activities.ProjectK8sActivities
	var dbActs *activities.ProjectDBActivities

	err := workflow.ExecuteActivity(ctx, k8sActs.DeleteNamespace, projectName).Get(ctx, nil)
	if err != nil {
		return err
	}
	err = workflow.ExecuteActivity(ctx, dbActs.DeleteProjectDBActivity, projectID).Get(ctx, nil)
	if err != nil {
		return err
	}

	return nil

}

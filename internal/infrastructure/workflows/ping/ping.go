package ping

import (
	"context"
	"time"

	"go.temporal.io/sdk/workflow"
)

func PingActivity(ctx context.Context, name string) (string, error) {
	return "Pong " + name + " from Temporal!", nil
}

func PingWorkflow(ctx workflow.Context, name string) (string, error) {
	option := workflow.ActivityOptions{
		StartToCloseTimeout: time.Second * 5,
	}

	ctx = workflow.WithActivityOptions(ctx, option)

	var result string
	err := workflow.ExecuteActivity(ctx, PingActivity, name).Get(ctx, &result)
	return result, err
}

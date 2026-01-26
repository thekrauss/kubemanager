package workflows

import (
	"fmt"
	"time"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type DeployWorkloadInput struct {
	WorkloadID         string
	ProjectID          string
	Namespace          string
	ReleaseName        string
	Image              string
	EnvVars            map[string]string
	Secrets            map[string]string
	PersistenceEnabled bool
	StorageSize        string
	StorageClass       string
	Replicas           int
	ServiceType        string //"ClusterIP" ou "LoadBalancer"
	TargetPort         int
}

func DeployWorkloadWorkflow(ctx workflow.Context, input DeployWorkloadInput) error {
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

	var dbActs *activities.WorkloadDBActivities
	var helmActs *activities.WorkloadActivities

	//STATUT -> STARTING
	err := workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus, input.WorkloadID, "STARTING", "HELM_PREPARING").Get(ctx, nil)
	if err != nil {
		return err
	}

	var imgInfo activities.ImageInfo
	err = workflow.ExecuteLocalActivity(ctx, helmActs.ParseImage, input.Image).Get(ctx, &imgInfo)
	if err != nil {
		workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus, input.WorkloadID, "FAILED", "SECRET_CREATION_ERROR")
		return err
	}

	err = workflow.ExecuteActivity(ctx, helmActs.EnsureSecret, input.Namespace, input.ReleaseName, input.EnvVars).Get(ctx, nil)
	if err != nil {
		workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus, input.WorkloadID, "FAILED", "SECRET_CREATION_ERROR")
		return err
	}

	vpsIP := configs.AppConfig.Vps.AdressIp
	shortID := input.ProjectID[:6]
	externalURL := fmt.Sprintf("%s-%s.%s.sslip.io", input.ReleaseName, shortID, vpsIP)
	workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus, input.WorkloadID, "STARTING", "HELM_INSTALLING").Get(ctx, nil)

	helmInput := activities.InstallWorkloadInput{
		ReleaseName:        input.ReleaseName,
		Namespace:          input.Namespace,
		ImageRepo:          imgInfo.Repository,
		ImageTag:           imgInfo.Tag,
		ExternalURL:        externalURL,
		Env:                input.EnvVars,
		PersistenceEnabled: input.PersistenceEnabled,
		StorageSize:        input.StorageSize,
		StorageClass:       input.StorageClass,
		Replicas:           input.Replicas,
		ServiceType:        input.ServiceType,
		Secrets:            input.Secrets,
		TargetPort:         input.TargetPort,
	}
	err = workflow.ExecuteActivity(ctx, helmActs.InstallChart, helmInput).Get(ctx, nil)
	if err != nil {
		workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus, input.WorkloadID, "FAILED", "HELM_ERROR")
		return err
	}

	//FINITION -> RUNNING
	workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus, input.WorkloadID, "RUNNING", "DEPLOYED").Get(ctx, nil)

	return nil
}

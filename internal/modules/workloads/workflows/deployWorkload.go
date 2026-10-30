package workflows

import (
	"time"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type DeployWorkloadInput struct {
	// Identity : identité du workload (nil : aucune).
	Identity *activities.WorkloadIdentity

	WorkloadID  string
	ProjectID   string
	Namespace   string
	ReleaseName string
	Image       string

	EnvVars map[string]string
	Secrets map[string]string

	// Routage public, calculé par le service (enregistré avec le workload) :
	// ExternalHost vide = pas d'ingress.
	ExternalHost     string
	IngressClass     string
	IngressNamespace string

	PersistenceEnabled bool
	StorageSize        string
	StorageClass       string
	MountPath          string

	Replicas    int
	ServiceType string // "ClusterIP" ou "LoadBalancer"
	TargetPort  int

	// Resources
	CPURequest    string
	MemoryRequest string
	CPULimit      string
	MemoryLimit   string

	// Security overrides
	RunAsUser      int64
	RunAsGroup     int64
	ReadOnlyRootFS bool

	// Autoscaling
	AutoscalingEnabled   bool
	AutoscalingMin       int32
	AutoscalingMax       int32
	AutoscalingTargetCPU int32

	// PDB + TLS
	PDBEnabled bool
	TLSEnabled bool

	// Probes
	HealthPath    string
	ReadinessPath string
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

	err := workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
		input.WorkloadID, input.ProjectID, "STARTING", "HELM_PREPARING").Get(ctx, nil)
	if err != nil {
		return err
	}

	var imgInfo activities.ImageInfo
	if err := workflow.SideEffect(ctx, func(workflow.Context) interface{} {
		info, _ := activities.ParseImage(input.Image)
		return info
	}).Get(&imgInfo); err != nil {
		workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
			input.WorkloadID, input.ProjectID, "FAILED", "IMAGE_PARSE_ERROR")
		return err
	}

	secretData := mergeMaps(input.EnvVars, input.Secrets)
	err = workflow.ExecuteActivity(ctx, helmActs.EnsureSecret,
		input.Namespace, input.ReleaseName, input.ProjectID, secretData).Get(ctx, nil)
	if err != nil {
		workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
			input.WorkloadID, input.ProjectID, "FAILED", "SECRET_CREATION_ERROR")
		return err
	}

	workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
		input.WorkloadID, input.ProjectID, "STARTING", "HELM_INSTALLING").Get(ctx, nil)

	helmInput := activities.InstallWorkloadInput{
		ProjectID:            input.ProjectID,
		WorkloadID:           input.WorkloadID,
		ReleaseName:          input.ReleaseName,
		Namespace:            input.Namespace,
		ImageRepo:            imgInfo.Repository,
		ImageTag:             imgInfo.Tag,
		ExternalURL:          input.ExternalHost,
		IngressClass:         input.IngressClass,
		IngressNamespace:     input.IngressNamespace,
		Env:                  input.EnvVars,
		Secrets:              input.Secrets,
		PersistenceEnabled:   input.PersistenceEnabled,
		StorageSize:          input.StorageSize,
		StorageClass:         input.StorageClass,
		MountPath:            input.MountPath,
		Replicas:             input.Replicas,
		ServiceType:          input.ServiceType,
		TargetPort:           input.TargetPort,
		CPURequest:           input.CPURequest,
		MemoryRequest:        input.MemoryRequest,
		CPULimit:             input.CPULimit,
		MemoryLimit:          input.MemoryLimit,
		RunAsUser:            input.RunAsUser,
		RunAsGroup:           input.RunAsGroup,
		ReadOnlyRootFS:       input.ReadOnlyRootFS,
		AutoscalingEnabled:   input.AutoscalingEnabled,
		AutoscalingMin:       input.AutoscalingMin,
		AutoscalingMax:       input.AutoscalingMax,
		AutoscalingTargetCPU: input.AutoscalingTargetCPU,
		PDBEnabled:           input.PDBEnabled,
		TLSEnabled:           input.TLSEnabled,
		HealthPath:           input.HealthPath,
		ReadinessPath:        input.ReadinessPath,
		Identity:             input.Identity,
	}
	err = workflow.ExecuteActivity(ctx, helmActs.InstallChart, helmInput).Get(ctx, nil)
	if err != nil {
		workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
			input.WorkloadID, input.ProjectID, "FAILED", "HELM_ERROR")
		return err
	}

	workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
		input.WorkloadID, input.ProjectID, "RUNNING", "DEPLOYED").Get(ctx, nil)

	return nil
}

func mergeMaps(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

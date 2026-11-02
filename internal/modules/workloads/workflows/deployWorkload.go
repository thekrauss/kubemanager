package workflows

import (
	"github.com/thekrauss/kubemanager/internal/modules/builds/registry"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
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
	// PlatformImage : image du registre de la plateforme (Secret de pull requis).
	PlatformImage bool

	// EnvRevision : version des variables à déployer. Les valeurs ne passent
	// jamais par ici : tout ce qu'un workflow reçoit reste dans son historique.
	EnvRevision int64

	// CustomDomains : noms vérifiés à router vers ce conteneur.
	CustomDomains   []string
	CustomDomainTLS bool

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
	retry := &temporal.RetryPolicy{
		InitialInterval: activityRetryInitialInterval,
		MaximumAttempts: activityRetryMaximumAttempts,
	}
	// WaitForCancellation : annulé, le workflow ne se ferme qu'une fois l'activité
	// terminée. Rien n'écrit plus dans le cluster quand la suppression commence.
	options := workflow.ActivityOptions{
		StartToCloseTimeout: activityStartToCloseTimeout,
		WaitForCancellation: true,
		RetryPolicy:         retry,
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	install := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: installActivityTimeout,
		HeartbeatTimeout:    installHeartbeatTimeout,
		WaitForCancellation: true,
		RetryPolicy:         retry,
	})

	localOptions := workflow.LocalActivityOptions{
		StartToCloseTimeout: localActivityStartToCloseTimeout,
	}
	ctx = workflow.WithLocalActivityOptions(ctx, localOptions)

	var dbActs *activities.WorkloadDBActivities
	var helmActs *activities.WorkloadActivities

	// finalStatus : sur un contexte détaché, pour que l'état soit écrit même
	// après une annulation. L'écriture est conditionnelle en base : elle ne
	// remplace jamais DELETING.
	finalStatus := func(status, phase string) error {
		cleanup, cancel := workflow.NewDisconnectedContext(ctx)
		defer cancel()
		cleanup = workflow.WithActivityOptions(cleanup, workflow.ActivityOptions{
			StartToCloseTimeout: activityStartToCloseTimeout,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval: activityRetryInitialInterval,
				MaximumAttempts: finalStatusMaximumAttempts,
			},
		})
		return workflow.ExecuteActivity(cleanup, dbActs.UpdateWorkloadStatus,
			input.WorkloadID, input.ProjectID, status, phase).Get(cleanup, nil)
	}
	fail := func(phase string, cause error) error {
		if err := finalStatus(utils.WorkloadFailed, phase); err != nil {
			workflow.GetLogger(ctx).Warn("workload failure not recorded", "workload", input.WorkloadID, "error", err)
		}
		return cause
	}

	err := workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
		input.WorkloadID, input.ProjectID, utils.WorkloadStarting, utils.PhaseHelmPreparing).Get(ctx, nil)
	if err != nil {
		return fail(utils.PhaseHelmError, err)
	}

	imgInfo, err := activities.ParseImage(input.Image)
	if err != nil {
		return fail(utils.PhaseImageParseError, err)
	}

	err = workflow.ExecuteActivity(ctx, helmActs.SyncEnvSecret,
		input.Namespace, input.ReleaseName, input.ProjectID, input.WorkloadID, input.EnvRevision).Get(ctx, nil)
	if err != nil {
		return fail(utils.PhaseSecretCreateError, err)
	}

	var pullSecrets []string
	if input.PlatformImage {
		if err := workflow.ExecuteActivity(ctx, helmActs.EnsureRegistryPullSecret, input.Namespace, input.ProjectID).Get(ctx, nil); err != nil {
			return fail(utils.PhaseSecretCreateError, err)
		}
		pullSecrets = []string{registry.PullSecretName}
	}

	err = workflow.ExecuteActivity(ctx, dbActs.UpdateWorkloadStatus,
		input.WorkloadID, input.ProjectID, utils.WorkloadStarting, utils.PhaseHelmReleaseInstalling).Get(ctx, nil)
	if err != nil {
		return fail(utils.PhaseHelmError, err)
	}

	helmInput := activities.InstallWorkloadInput{
		ProjectID:            input.ProjectID,
		WorkloadID:           input.WorkloadID,
		ReleaseName:          input.ReleaseName,
		Namespace:            input.Namespace,
		ImageRepo:            imgInfo.Repository,
		ImageTag:             imgInfo.Tag,
		PullSecrets:          pullSecrets,
		ExternalURL:          input.ExternalHost,
		IngressClass:         input.IngressClass,
		IngressNamespace:     input.IngressNamespace,
		EnvRevision:          input.EnvRevision,
		CustomDomains:        input.CustomDomains,
		CustomDomainTLS:      input.CustomDomainTLS,
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
	err = workflow.ExecuteActivity(install, helmActs.InstallChart, helmInput).Get(install, nil)
	if err != nil {
		return fail(utils.PhaseHelmError, err)
	}

	// La release est en place : RUNNING est vrai même si une annulation arrive
	// maintenant. Sans cet enregistrement, le conteneur resterait STARTING.
	if err := finalStatus(utils.WorkloadRunning, utils.PhaseDeployed); err != nil {
		return fail(utils.PhaseHelmError, err)
	}
	return nil
}

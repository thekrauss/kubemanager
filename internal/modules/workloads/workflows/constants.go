package workflows

import "time"

// Noms des workflows Temporal (nom de la fonction enregistrée par le worker).
const (
	DeployWorkloadWorkflowName = "DeployWorkloadWorkflow"
	DeleteWorkloadWorkflowName = "DeleteWorkloadWorkflow"
)

// Préfixes des identifiants de workflow (suivis de l'identifiant du workload) :
// un même identifiant empêche deux exécutions concurrentes.
const (
	WorkflowIDDeployPrefix = "workload-deploy-"
	WorkflowIDUpdatePrefix = "workload-update-"
	WorkflowIDDeletePrefix = "workload-delete-"
)

// Politique d'exécution des activities des workflows de workload.
const (
	activityStartToCloseTimeout      = 5 * time.Minute
	activityRetryInitialInterval     = time.Second
	activityRetryMaximumAttempts     = 3
	localActivityStartToCloseTimeout = 5 * time.Second
)

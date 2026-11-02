package workflows

import (
	"time"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
)

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

	// finalStatusMaximumAttempts : l'état final doit survivre à une courte panne
	// de la base (intervalle doublé : environ quatre minutes d'essais) ; un
	// conteneur resté STARTING compterait dans le quota sans jamais démarrer.
	finalStatusMaximumAttempts = 9

	// installActivityTimeout : Helm s'arrête de lui-même bien avant : Temporal
	// ne relance jamais une tentative pendant que la précédente tourne.
	installActivityTimeout = activities.HelmInstallTimeout + 2*time.Minute
	// installHeartbeatTimeout : Temporal transmet les heartbeats à 80 % de ce
	// délai, et l'annulation n'arrive à l'activité qu'avec eux.
	installHeartbeatTimeout = 10 * time.Second
)

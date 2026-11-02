package workflows

import "time"

// Politique d'exécution des activities des workflows de site.
const (
	activityStartToCloseTimeout  = 10 * time.Minute
	activityRetryInitialInterval = time.Second
	activityRetryMaximumInterval = time.Minute
	activityRetryMaximumAttempts = 3
)

// Préfixes des identifiants de workflow.
const (
	// WorkflowIDDeployPrefix : suivi de l'identifiant du déploiement.
	WorkflowIDDeployPrefix = "site-deploy-"
	// WorkflowIDDeletePrefix : suivi de l'identifiant du site.
	WorkflowIDDeletePrefix = "site-delete-"
)

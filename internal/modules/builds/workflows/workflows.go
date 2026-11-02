// Package workflows : orchestration d'un build.
package workflows

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/thekrauss/kubemanager/internal/modules/builds/activities"
	"github.com/thekrauss/kubemanager/internal/modules/builds/domain"
)

// WorkflowIDPrefix : suivi de l'identifiant du build.
const WorkflowIDPrefix = "build-"

const (
	stepTimeout     = 5 * time.Minute
	stepMaxAttempts = 3
	// heartbeatTimeout : Temporal transmet les heartbeats à 80 % de ce délai,
	// et l'annulation n'arrive à l'activité qu'avec eux : à une minute, annuler
	// un build prenait 48 s.
	heartbeatTimeout = 10 * time.Second
	runMaxAttempts   = 2
	finishTimeout    = 5 * time.Minute

	// Déploiement enchaîné : un déploiement en cours peut durer quelques
	// minutes (Helm attend les pods). Environ dix minutes d'essais, puis l'état
	// BUSY reste visible sur le build.
	deployTimeout     = time.Minute
	deployFirstRetry  = 15 * time.Second
	deployMaxRetry    = time.Minute
	deployMaxAttempts = 12
)

// BuildInput : build et durée maximale du Job.
type BuildInput struct {
	activities.BuildRef
	Deadline time.Duration
	// Deploy : le serveur met le conteneur du build à jour à la fin (voie GitHub).
	Deploy bool
}

// BuildImageWorkflow : lance le Job et enregistre le résultat. Quelle que soit
// l'issue (erreur, annulation), Finish conserve le journal et fait le ménage.
func BuildImageWorkflow(ctx workflow.Context, in BuildInput) error {
	var acts *activities.Activities
	step := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: stepTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: stepMaxAttempts},
	})
	run := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: in.Deadline + stepTimeout,
		HeartbeatTimeout:    heartbeatTimeout,
		WaitForCancellation: true,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: runMaxAttempts},
	})

	var res activities.BuildResult
	err := workflow.ExecuteActivity(step, acts.MarkBuilding, in.BuildRef).Get(step, nil)
	if err == nil {
		err = workflow.ExecuteActivity(run, acts.RunBuild, in.BuildRef).Get(run, &res)
	}

	final := activities.FinishInput{BuildRef: in.BuildRef, Status: domain.StatusSucceeded, Digest: res.Digest, Detected: res.Detected}
	if err != nil {
		final.Status, final.Failure = outcome(err)
	}
	cleanup, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	cleanup = workflow.WithActivityOptions(cleanup, workflow.ActivityOptions{
		StartToCloseTimeout: finishTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: stepMaxAttempts},
	})
	if finishErr := workflow.ExecuteActivity(cleanup, acts.Finish, final).Get(cleanup, nil); finishErr != nil && err == nil {
		return finishErr
	}
	if in.Deploy {
		deployAfterBuild(cleanup, in, final)
	}
	if temporal.IsCanceledError(err) {
		return nil
	}
	return err
}

// deployAfterBuild : réussite → le conteneur passe à la nouvelle image ; échec
// ou annulation → rien n'est déployé, l'actuel reste en service. Sur un contexte
// détaché : annuler un build déjà terminé n'interrompt pas son déploiement
// (DeployBuilt vérifie qu'aucun build plus récent n'existe).
func deployAfterBuild(ctx workflow.Context, in BuildInput, final activities.FinishInput) {
	var acts *activities.Activities
	if final.Status != domain.StatusSucceeded {
		_ = workflow.ExecuteActivity(ctx, acts.BuildNotDeployed, activities.NotDeployedInput{BuildRef: in.BuildRef, Status: final.Status}).Get(ctx, nil)
		return
	}
	deploy := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: deployTimeout,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: deployFirstRetry, BackoffCoefficient: 2,
			MaximumInterval: deployMaxRetry, MaximumAttempts: deployMaxAttempts},
	})
	err := workflow.ExecuteActivity(deploy, acts.DeployBuilt, in.BuildRef).Get(deploy, nil)
	if err == nil {
		return
	}
	state := domain.DeployFailed
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.Type() == domain.DeployBusy {
		state = domain.DeployBusy
	}
	_ = workflow.ExecuteActivity(ctx, acts.RecordDeployFailure, activities.DeployFailedInput{BuildRef: in.BuildRef, State: state}).Get(ctx, nil)
}

func outcome(err error) (status, failure string) {
	if temporal.IsCanceledError(err) {
		return domain.StatusCancelled, ""
	}
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		switch appErr.Type() {
		case domain.FailureBuild, domain.FailureTimeout, domain.FailureDisk, domain.FailureNoPlan, domain.FailureGitAccess:
			return domain.StatusFailed, appErr.Type()
		}
	}
	return domain.StatusFailed, domain.FailureInternal
}

package workflows

import (
	"errors"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/thekrauss/kubemanager/internal/modules/sites/activities"
	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
)

const genericDeployError = "le déploiement a échoué, réessayez"

func activityCtx(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: activityStartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: activityRetryInitialInterval,
			MaximumInterval: activityRetryMaximumInterval,
			MaximumAttempts: activityRetryMaximumAttempts,
		},
	})
}

// DeploySiteWorkflow : archive uploadée → fichiers extraits → déploiement actif.
//
// Compensation : en cas d'échec, le déploiement passe FAILED avec un message
// lisible ; le déploiement actif précédent reste servi (rien n'est basculé
// avant la fin de l'extraction). Les fichiers partiellement extraits restent
// sous le préfixe du déploiement échoué et partent avec la suppression du site.
func DeploySiteWorkflow(ctx workflow.Context, ref activities.DeploymentRef) error {
	ctx = activityCtx(ctx)
	var acts *activities.SiteActivities

	var res activities.ExtractResult
	err := workflow.ExecuteActivity(ctx, acts.ExtractDeployment, ref).Get(ctx, &res)
	if err == nil {
		err = workflow.ExecuteActivity(ctx, acts.MarkDeploymentReady, activities.ReadyInput{
			DeploymentRef: ref, FileCount: res.FileCount, SizeBytes: res.SizeBytes,
		}).Get(ctx, nil)
	}

	// L'archive n'est plus utile, succès ou échec.
	_ = workflow.ExecuteActivity(ctx, acts.DeleteArchive, ref).Get(ctx, nil)

	if err != nil {
		_ = workflow.ExecuteActivity(ctx, acts.MarkDeploymentFailed, activities.FailedInput{
			DeploymentRef: ref, Message: userMessage(err),
		}).Get(ctx, nil)
		return err
	}
	return nil
}

// userMessage : le message d'une erreur InvalidArchive est rédigé pour
// l'utilisateur ; toute autre erreur (infra) reste interne.
func userMessage(err error) string {
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.Type() == domain.ErrTypeInvalidArchive {
		return appErr.Message()
	}
	return genericDeployError
}

// DeleteSiteWorkflow : fichiers d'abord, ligne DB ensuite (même logique que
// "Helm first, DB second" : jamais d'objets orphelins sans propriétaire en DB).
// Le site est déjà en DELETING (l'edge ne le sert plus) quand le workflow démarre.
func DeleteSiteWorkflow(ctx workflow.Context, siteID string) error {
	ctx = activityCtx(ctx)
	var acts *activities.SiteActivities

	if err := workflow.ExecuteActivity(ctx, acts.DeleteSiteObjects, siteID).Get(ctx, nil); err != nil {
		return err
	}
	return workflow.ExecuteActivity(ctx, acts.DeleteSiteRecord, siteID).Get(ctx, nil)
}

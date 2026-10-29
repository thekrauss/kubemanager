package workflows

import (
	"time"

	"github.com/thekrauss/kubemanager/internal/modules/projects/activities"
	siteActivities "github.com/thekrauss/kubemanager/internal/modules/sites/activities"
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

	// 1. Namespace K8s : cascade delete de toutes les ressources (Deployments,
	//    Services, Ingress, PVCs, Helm storage Secrets…). Idempotent sur NotFound.
	if err := workflow.ExecuteActivity(ctx, k8sActs.DeleteNamespace, projectName).Get(ctx, nil); err != nil {
		return err
	}

	// 2. DB : hard-delete des workload rows du projet pour éviter les orphelins
	//    affichés dans le dashboard (les ressources K8s sont déjà parties en 1).
	if err := workflow.ExecuteActivity(ctx, dbActs.DeleteProjectWorkloadsActivity, projectID).Get(ctx, nil); err != nil {
		return err
	}

	// 3. Sites statiques : leurs lignes partent avec le projet (FK CASCADE) mais
	//    pas leurs fichiers dans l'object store → on les supprime avant.
	var siteActs *siteActivities.SiteActivities
	if err := workflow.ExecuteActivity(ctx, siteActs.DeleteProjectSiteObjects, projectID).Get(ctx, nil); err != nil {
		return err
	}

	// 4. DB : delete project + membres (CASCADE FK sur project_members et sites).
	if err := workflow.ExecuteActivity(ctx, dbActs.DeleteProjectDBActivity, projectID).Get(ctx, nil); err != nil {
		return err
	}

	return nil
}

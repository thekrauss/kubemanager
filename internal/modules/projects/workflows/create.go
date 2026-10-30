package workflows

import (
	"context"
	"fmt"

	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ProjectActivities : stub local utilisé UNIQUEMENT pour dériver les noms
// d'activities via reflection (pattern Temporal). Les vraies implémentations
// sont dans activities/{db,k8s}.go et enregistrées via processor.go.
//
// Chaque méthode ici DOIT avoir le même nom ET la même signature qu'une
// méthode sur une activity struct enregistrée, sinon Temporal rejette
// l'activity au runtime.
type ProjectActivities struct{}

func (a *ProjectActivities) CreateProjectInDB(ctx context.Context, projectID, name, description, ownerID string) (*dauth.Project, error) {
	return nil, nil
}
func (a *ProjectActivities) CreateNamespace(ctx context.Context, projectID string, projectName string) error {
	return nil
}

func (a *ProjectActivities) DeleteProjectDBActivity(ctx context.Context, projectID string) error {
	return nil
}

func (a *ProjectActivities) UpdateProjectStatusActivity(ctx context.Context, projectID, status string) error {
	return nil
}

func (a *ProjectActivities) ReconcileProjectResources(ctx context.Context, nsName, cpu, mem string) error {
	return nil
}

// AssignDefaultOwner : signature MATCHÉE sur ProjectK8sActivities.AssignDefaultOwner
// (fix #4 : avant on avait `AssignProjectRole(userID, projectID, roleName)` qui
// n'était enregistrée nulle part → "activity not registered" au runtime).
func (a *ProjectActivities) AssignDefaultOwner(ctx context.Context, projectID, userID string) error {
	return nil
}

type CreateProjectInput struct {
	// ProjectID : identifiant choisi par CreateProject (renvoyé à l'appelant
	// avant la fin du workflow).
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	OwnerID string `json:"owner_id"`

	CpuLimit    string `json:"cpu_limit"`
	MemoryLimit string `json:"memory_limit"`
}

type ProjectResult struct {
	ProjectID string `json:"project_id"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

func CreateProjectWorkflow(ctx workflow.Context, input CreateProjectInput) (ProjectResult, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: activityStartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: activityRetryInitialInterval,
			MaximumAttempts: activityRetryMaximumAttempts,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	localOptions := workflow.LocalActivityOptions{
		StartToCloseTimeout: localActivityStartToCloseTimeout,
	}
	ctx = workflow.WithLocalActivityOptions(ctx, localOptions)

	var a *ProjectActivities
	var dbRes dauth.Project
	var result ProjectResult

	err := workflow.ExecuteLocalActivity(ctx, a.CreateProjectInDB, input.ProjectID, input.Name, input.Description, input.OwnerID).Get(ctx, &dbRes)
	if err != nil {
		return result, err
	}
	result.ProjectID = dbRes.ID.String()
	// Fix #3 : calcule nsName UNE fois, utilise-le pour ReconcileProjectResources
	// (avant : result.Namespace lu ligne 87 avant d'être setté → "" passé en paramètre).
	result.Namespace = utils.ProjectNamespace(input.Name)

	err = workflow.ExecuteActivity(ctx, a.CreateNamespace, result.ProjectID, input.Name).Get(ctx, nil)
	if err != nil {
		compensateOptions := workflow.LocalActivityOptions{StartToCloseTimeout: compensationTimeout}
		ctxComp := workflow.WithLocalActivityOptions(ctx, compensateOptions)

		_ = workflow.ExecuteLocalActivity(ctxComp, a.DeleteProjectDBActivity, result.ProjectID).Get(ctx, nil)
		return result, fmt.Errorf("failed to provision k8s namespace, rolled back: %w", err)
	}
	// Après le namespace, un échec laisse le projet visible en ERROR (au lieu
	// d'un PENDING éternel) ; le statut est enregistré au mieux.
	markError := func(err error) (ProjectResult, error) {
		_ = workflow.ExecuteLocalActivity(ctx, a.UpdateProjectStatusActivity, result.ProjectID, utils.ProjectStatusError).Get(ctx, nil)
		result.Status = utils.ProjectStatusError
		return result, err
	}
	err = workflow.ExecuteActivity(ctx, a.ReconcileProjectResources, result.Namespace, input.CpuLimit, input.MemoryLimit).Get(ctx, nil)
	if err != nil {
		return markError(err)
	}

	// Fix #4 : appelle la VRAIE activity enregistrée (ProjectK8sActivities.AssignDefaultOwner).
	// Signature : (ctx, projectID, userID).
	err = workflow.ExecuteActivity(ctx, a.AssignDefaultOwner, result.ProjectID, input.OwnerID).Get(ctx, nil)
	if err != nil {
		return markError(err)
	}

	if err := workflow.ExecuteLocalActivity(ctx, a.UpdateProjectStatusActivity, result.ProjectID, utils.ProjectStatusReady).Get(ctx, nil); err != nil {
		return result, err
	}
	result.Status = utils.ProjectStatusReady
	return result, nil
}

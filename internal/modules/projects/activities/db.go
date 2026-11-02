package activities

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	workloadRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
)

type ProjectDBActivities struct {
	Repo         repository.ProjectRepository
	WorkloadRepo workloadRepo.WorkloadRepository
	Logger       *zap.SugaredLogger
}

// CreateProjectInDB : projectID est choisi par l'appelant (CreateProject le
// renvoie avant la fin du workflow) ; vide : généré ici.
func (a *ProjectDBActivities) CreateProjectInDB(ctx context.Context, projectID, name, description, ownerID string) (*dauth.Project, error) {
	project := &dauth.Project{
		Name:        name,
		Description: description,
	}
	if id, err := uuid.Parse(projectID); err == nil {
		project.ID = id
	}

	err := a.Repo.CreateProject(ctx, project, ownerID)
	if err != nil {
		return nil, err
	}
	return project, nil
}

// UpdateProjectStatusActivity : statut visible par la console (READY en fin
// de provisionnement, ERROR si une étape échoue).
func (a *ProjectDBActivities) UpdateProjectStatusActivity(ctx context.Context, projectID, status string) error {
	return a.Repo.UpdateProjectStatus(ctx, projectID, status)
}

func (a *ProjectDBActivities) DeleteProjectDBActivity(ctx context.Context, projectID string) error {
	a.Logger.Warnw("Rolling back project creation in DB", "projectID", projectID)
	err := a.Repo.DeleteProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("failed to rollback project in DB: %w", err)
	}
	return nil
}

// DeleteProjectWorkloadsActivity : hard-deletes every workload row tied to
// the project. Called by DeleteProjectWorkflow after the K8s namespace has
// been torn down (namespace cascade already drops the actual Helm resources,
// but DB rows would otherwise linger and show up as "orphan" workloads in
// the dashboard).
func (a *ProjectDBActivities) DeleteProjectWorkloadsActivity(ctx context.Context, projectID string) (int64, error) {
	if a.WorkloadRepo == nil {
		a.Logger.Warnw("WorkloadRepo not wired on ProjectDBActivities — skipping workload cleanup")
		return 0, nil
	}
	pID, err := uuid.Parse(projectID)
	if err != nil {
		return 0, fmt.Errorf("invalid project id: %w", err)
	}
	n, err := a.WorkloadRepo.DeleteByProject(ctx, pID)
	if err != nil {
		return 0, fmt.Errorf("delete workloads for project %s: %w", projectID, err)
	}
	a.Logger.Infow("project workloads hard-deleted", "projectID", projectID, "count", n)
	return n, nil
}

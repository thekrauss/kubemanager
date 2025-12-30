package activities

import (
	"context"
	"fmt"

	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"go.uber.org/zap"
)

type ProjectDBActivities struct {
	Repo   repository.ProjectRepository
	Logger *zap.SugaredLogger
}

func (a *ProjectDBActivities) CreateProjectInDB(ctx context.Context, name string, description string, ownerID string) (*dauth.Project, error) {
	project := &dauth.Project{
		Name:        name,
		Description: description,
	}

	err := a.Repo.CreateProject(ctx, project, ownerID)
	if err != nil {
		return nil, err
	}
	return project, nil
}

func (a *ProjectDBActivities) DeleteProjectDBActivity(ctx context.Context, projectID string) error {
	a.Logger.Warnw("Rolling back project creation in DB", "projectID", projectID)
	err := a.Repo.DeleteProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("failed to rollback project in DB: %w", err)
	}
	return nil
}

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/modules/sites/activities"
	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
	"github.com/thekrauss/kubemanager/internal/modules/sites/repository"
)

type compRepo struct {
	repository.SiteRepository
	site     domain.Site
	statuses []string
	depFail  []string
}

func (r *compRepo) GetSite(context.Context, uuid.UUID, uuid.UUID) (*domain.Site, error) {
	s := r.site
	return &s, nil
}
func (r *compRepo) SetSiteStatus(ctx context.Context, _ uuid.UUID, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.statuses = append(r.statuses, status)
	return nil
}
func (*compRepo) CreateDeployment(context.Context, *domain.SiteDeployment) error { return nil }
func (r *compRepo) UpdateDeploymentStatus(ctx context.Context, _ uuid.UUID, status, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.depFail = append(r.depFail, status)
	return nil
}

type compStore struct {
	objectstore.Store
	deleted []string
	putErr  error
}

func (s *compStore) Put(_ context.Context, _ string, r io.Reader, _ int64, _ string) error {
	_, _ = io.Copy(io.Discard, r)
	return s.putErr
}
func (s *compStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.deleted = append(s.deleted, key)
	return nil
}

type compDist struct {
	temporal.TaskDistributor
	cancel func()
	err    error
}

func (d compDist) ExecuteWorkflow(context.Context, temporal.ExecuteOptions, any, ...any) (temporal.WorkflowHandle, error) {
	if d.cancel != nil {
		d.cancel()
	}
	return temporal.WorkflowHandle{}, d.err
}

func compService(repo *compRepo, store *compStore, dist compDist) *SiteService {
	return NewSiteService(repo, store, dist, nil, Config{Limits: activities.Limits{MaxArchiveBytes: 1 << 20}}, zap.NewNop().Sugar())
}

// Déploiement non lancé alors que la requête est interrompue : marqué en échec
// et sans archive orpheline.
func TestDeploy_StartFailureCompensates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	down := errors.New("temporal down")
	repo, store := &compRepo{site: domain.Site{ID: uuid.New(), Status: domain.SiteStatusLive}}, &compStore{}
	s := compService(repo, store, compDist{cancel: cancel, err: down})
	_, err := s.Deploy(ctx, uuid.New(), repo.site.ID, nil, strings.NewReader("zip"), 3)
	require.ErrorIs(t, err, down)
	assert.Equal(t, []string{domain.DeploymentStatusFailed}, repo.depFail)
	assert.Len(t, store.deleted, 1)
}

// Suppression non lancée : le site retrouve son statut et reste servi.
func TestDeleteSite_StartFailureRestoresStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	down := errors.New("temporal down")
	repo := &compRepo{site: domain.Site{ID: uuid.New(), Status: domain.SiteStatusLive}}
	s := compService(repo, &compStore{}, compDist{cancel: cancel, err: down})
	require.ErrorIs(t, s.DeleteSite(ctx, uuid.New(), repo.site.ID), down)
	assert.Equal(t, []string{domain.SiteStatusDeleting, domain.SiteStatusLive}, repo.statuses)
}

// Deuxième demande de suppression : déjà en cours, pas d'erreur.
func TestDeleteSite_AlreadyRunning(t *testing.T) {
	repo := &compRepo{site: domain.Site{ID: uuid.New(), Status: domain.SiteStatusDeleting}}
	s := compService(repo, &compStore{}, compDist{err: fmt.Errorf("execute workflow: %w", serviceerror.NewWorkflowExecutionAlreadyStarted("started", "", "run"))})
	require.NoError(t, s.DeleteSite(context.Background(), uuid.New(), repo.site.ID))
}

package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	"github.com/thekrauss/kubemanager/internal/modules/builds/repository"
)

type startRepo struct {
	repository.BuildRepository
	finished []string
}

func (*startRepo) CountActive(context.Context, uuid.UUID) (int64, error) { return 0, nil }
func (*startRepo) Create(context.Context, *domain.Build) error           { return nil }
func (r *startRepo) Finish(ctx context.Context, _ uuid.UUID, status, _, _, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.finished = append(r.finished, status)
	return nil
}

type startStore struct {
	objectstore.Store
	put, deleted []string
}

func (s *startStore) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	_, _ = io.Copy(io.Discard, r)
	s.put = append(s.put, key)
	return nil
}

func (s *startStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.deleted = append(s.deleted, key)
	return nil
}

// failingStart : Temporal indisponible et requête interrompue en même temps.
type failingStart struct {
	temporal.TaskDistributor
	cancel func()
	err    error
}

func (f failingStart) ExecuteWorkflow(context.Context, temporal.ExecuteOptions, any, ...any) (temporal.WorkflowHandle, error) {
	f.cancel()
	return temporal.WorkflowHandle{}, f.err
}

// Le build ne reste pas QUEUED à vie (il compterait toujours dans
// ErrTooManyBuilds) et son archive ne reste pas orpheline, même si le contexte de
// la requête est déjà annulé.
func TestFromArchive_StartFailureCompensates(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("contenu"))
	require.NoError(t, zw.Close())

	down := errors.New("temporal down")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo, store := &startRepo{}, &startStore{}
	s := &Service{Repo: repo, Store: store, Distributor: failingStart{cancel: cancel, err: down}, Cfg: Config{MaxConcurrent: 5, RegistryHost: "r.example"}}

	b, err := s.FromArchive(ctx, uuid.New(), "web", "", nil, &gz, int64(gz.Len()))
	require.ErrorIs(t, err, down)
	assert.Nil(t, b)
	assert.Equal(t, []string{domain.StatusFailed}, repo.finished)
	assert.Equal(t, store.put, store.deleted, "archive retirée")
}

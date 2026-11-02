package router

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/iam"
)

type countingStore struct {
	org      uuid.UUID
	bindings []iam.Binding
	calls    int
}

func (s *countingStore) BindingsFor(context.Context, []iam.Principal) ([]iam.Binding, error) {
	s.calls++
	return s.bindings, nil
}

func (s *countingStore) ProjectOrganization(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.org, nil
}

// Un retrait de droit publié par un autre process (worker, autre réplica)
// invalide le cache local : l'accès est refusé sans attendre le TTL.
//
//	docker run -d --rm --name iam-e2e-redis -p 127.0.0.1:56379:6379 redis:7-alpine
//	IAM_E2E=1 go test ./internal/infrastructure/router/ -run TestIAMSync -v
func TestIAMSync_RemoteRevocation(t *testing.T) {
	if os.Getenv("IAM_E2E") == "" {
		t.Skip("IAM_E2E not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:56379"})
	require.NoError(t, rdb.Ping(ctx).Err())

	project := uuid.New()
	alice := iam.UserPrincipal(uuid.New())
	store := &countingStore{org: uuid.New(), bindings: []iam.Binding{
		{Resource: iam.ProjectName(project), Role: "roles/owner", Principal: alice},
	}}
	app := &App{Redis: rdb, Logger: zap.NewNop().Sugar(), IAM: iam.NewEngine(store, time.Hour)}
	app.startIAMSync(ctx)
	time.Sleep(100 * time.Millisecond) // abonnement actif

	check := func() bool {
		d, err := app.IAM.Check(ctx, alice, iam.ProjectsDelete, iam.ProjectName(project))
		require.NoError(t, err)
		return d.Allowed
	}
	require.True(t, check())

	// Retrait fait par un autre process : la base change, puis il publie.
	store.bindings = nil
	require.NoError(t, rdb.Publish(ctx, iamInvalidateChannel, "other-process|"+string(alice)).Err())

	assert.Eventually(t, func() bool { return !check() }, 2*time.Second, 20*time.Millisecond,
		"le retrait doit être effectif bien avant le TTL d'une heure")
}

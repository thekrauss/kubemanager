package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/iam"
	buildsDomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	buildsSvc "github.com/thekrauss/kubemanager/internal/modules/builds/service"
	"github.com/thekrauss/kubemanager/internal/modules/github/client"
	"github.com/thekrauss/kubemanager/internal/modules/github/domain"
	"github.com/thekrauss/kubemanager/internal/modules/github/repository"
	workloadsDomain "github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
)

type fakeState map[string]string

func (f fakeState) SetValue(_ context.Context, k, v string, _ time.Duration) error {
	f[k] = v
	return nil
}
func (f fakeState) GetValue(_ context.Context, k string) (string, error) {
	if v, ok := f[k]; ok {
		return v, nil
	}
	return "", cache.ErrCacheMiss
}
func (f fakeState) TakeValue(c context.Context, k string) (string, error) {
	v, err := f.GetValue(c, k)
	delete(f, k)
	return v, err
}

type allow bool

func (a allow) Check(context.Context, iam.Principal, iam.Permission, iam.ResourceName) (iam.Decision, error) {
	return iam.Decision{Allowed: bool(a)}, nil
}

type fakeRepo struct {
	repository.Repository
	saved      []*domain.Installation
	links      []domain.Link
	lastBranch string
	claimed    map[string]bool
	gone       []int64
}

func (r *fakeRepo) UpsertInstallation(_ context.Context, in *domain.Installation) error {
	r.saved = append(r.saved, in)
	return nil
}
func (r *fakeRepo) LinksForPush(_ context.Context, _, _ int64, branch string) ([]domain.Link, error) {
	r.lastBranch = branch
	return r.links, nil
}
func (r *fakeRepo) RenameRepo(context.Context, int64, string) error { return nil }
func (r *fakeRepo) ClaimDelivery(_ context.Context, id, _ string) (bool, error) {
	if r.claimed[id] {
		return false, nil
	}
	if r.claimed == nil {
		r.claimed = map[string]bool{}
	}
	r.claimed[id] = true
	return true, nil
}
func (r *fakeRepo) InstallationGone(_ context.Context, id int64) error {
	r.gone = append(r.gone, id)
	return nil
}

type fakeBuilds struct {
	Builds
	reqs []buildsSvc.GitHubBuild
	err  error
}

func (f *fakeBuilds) FromGitHub(_ context.Context, req buildsSvc.GitHubBuild) (*buildsDomain.Build, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return &buildsDomain.Build{}, f.err
	}
	return &buildsDomain.Build{}, nil
}

type fakeWorkloads struct {
	Workloads
	failed int
}

func (f *fakeWorkloads) Get(_ context.Context, id uuid.UUID) (*workloadsDomain.Workload, error) {
	return &workloadsDomain.Workload{ID: id, Name: "web", Image: "img"}, nil
}
func (f *fakeWorkloads) MarkAwaitingBuild(context.Context, *workloadsDomain.Workload) error {
	return nil
}
func (f *fakeWorkloads) MarkBuildFailed(context.Context, uuid.UUID, uuid.UUID) error {
	f.failed++
	return nil
}

func newService(t *testing.T, gh http.Handler, authorized bool) (*Service, *fakeRepo, *fakeBuilds) {
	t.Helper()
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	raw := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cl, err := client.New(client.Config{AppID: "1", Slug: "s", ClientID: "c", ClientSecret: "x", PrivateKeyPEM: raw, APIURL: srv.URL, WebURL: srv.URL})
	require.NoError(t, err)
	repo, builds := &fakeRepo{}, &fakeBuilds{}
	return &Service{Client: cl, Repo: repo, State: fakeState{}, Authz: allow(authorized), Builds: builds,
		Workloads: &fakeWorkloads{}, Logger: zap.NewNop().Sugar()}, repo, builds
}

// visibleRepos : dépôts que la personne voit elle-même, sur les 3 de l'installation.
var visibleRepos = func() *int { n := 3; return &n }()

// Faux GitHub : cette personne n'a accès qu'à l'installation 1.
func githubWith(installations string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if strings.Contains(r.URL.RawQuery, "bad") {
				_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"ghu_x"}`))
		case "/user/installations":
			_, _ = w.Write([]byte(installations))
		case "/user/installations/1/repositories":
			_, _ = w.Write([]byte(fmt.Sprintf(`{"total_count":%d}`, *visibleRepos)))
		case "/app/installations/1/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_x"}`))
		case "/installation/repositories":
			_, _ = w.Write([]byte(`{"total_count":3,"repositories":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func stateFor(t *testing.T, s *Service, project, user uuid.UUID) string {
	t.Helper()
	u, err := s.InstallURL(context.Background(), project, user)
	require.NoError(t, err)
	_, state, ok := strings.Cut(u.URL, "state=")
	require.True(t, ok)
	return state
}

func TestCallback_InstallationOfSomeoneElseIsRefused(t *testing.T) {
	s, repo, _ := newService(t, githubWith(`{"installations":[{"id":1,"account":{"login":"awa","type":"User"}}]}`), true)
	project, user := uuid.New(), uuid.New()

	// Le navigateur annonce l'installation 2 : la personne n'y a pas accès.
	state := stateFor(t, s, project, user)
	_, err := s.Callback(context.Background(), user, "user:x", domain.CallbackRequest{State: state, InstallationID: 2, Code: "c"})
	require.ErrorIs(t, err, domain.ErrInstallationDenied)
	assert.Empty(t, repo.saved)

	// Le compte vient de GitHub, pas du navigateur.
	state = stateFor(t, s, project, user)
	res, err := s.Callback(context.Background(), user, "user:x", domain.CallbackRequest{State: state, InstallationID: 1, Code: "c"})
	require.NoError(t, err)
	assert.Equal(t, domain.CallbackLinked, res.Status)
	require.Len(t, repo.saved, 1)
	assert.Equal(t, "awa", repo.saved[0].AccountLogin)
	assert.Equal(t, project, repo.saved[0].ProjectID)
}

// Simple collaborateur d'un dépôt : lier l'installation lui donnerait tous les autres.
func TestCallback_PartialAccessIsRefused(t *testing.T) {
	*visibleRepos = 1
	defer func() { *visibleRepos = 3 }()
	s, repo, _ := newService(t, githubWith(`{"installations":[{"id":1,"account":{"login":"corp","type":"Organization"}}]}`), true)
	project, user := uuid.New(), uuid.New()
	_, err := s.Callback(context.Background(), user, "user:x", domain.CallbackRequest{State: stateFor(t, s, project, user), InstallationID: 1, Code: "c"})
	require.ErrorIs(t, err, domain.ErrInstallationPartial)
	assert.Empty(t, repo.saved)
}

func TestCallback_State(t *testing.T) {
	s, _, _ := newService(t, githubWith(`{"installations":[{"id":1,"account":{"login":"awa"}}]}`), true)
	project, user, other := uuid.New(), uuid.New(), uuid.New()
	req := func(state string) domain.CallbackRequest {
		return domain.CallbackRequest{State: state, InstallationID: 1, Code: "c"}
	}
	_, err := s.Callback(context.Background(), user, "user:x", req("inconnu"))
	require.ErrorIs(t, err, domain.ErrStateInvalid)

	state := stateFor(t, s, project, user)
	_, err = s.Callback(context.Background(), other, "user:y", req(state))
	require.ErrorIs(t, err, domain.ErrStateInvalid, "état d'un autre")

	_, err = s.Callback(context.Background(), user, "user:x", req(state))
	require.NoError(t, err, "l'essai d'un autre ne consomme pas l'état")

	_, err = s.Callback(context.Background(), user, "user:x", req(state))
	require.ErrorIs(t, err, domain.ErrStateInvalid, "usage unique")
}

func TestCallback_PermissionLostMeanwhile(t *testing.T) {
	s, repo, _ := newService(t, githubWith(`{"installations":[{"id":1,"account":{"login":"awa"}}]}`), false)
	project, user := uuid.New(), uuid.New()
	_, err := s.Callback(context.Background(), user, "user:x", domain.CallbackRequest{State: stateFor(t, s, project, user), InstallationID: 1, Code: "c"})
	require.ErrorIs(t, err, domain.ErrInstallationDenied)
	assert.Empty(t, repo.saved)
}

func pushBody(t *testing.T, mutate func(*domain.PushEvent)) []byte {
	t.Helper()
	e := domain.PushEvent{Ref: "refs/heads/main", After: strings.Repeat("a", 40)}
	e.Repository.ID, e.Repository.FullName, e.Installation.ID = 9, "awa/shop", 1
	if mutate != nil {
		mutate(&e)
	}
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	return raw
}

func TestWebhook_Push(t *testing.T) {
	s, repo, builds := newService(t, githubWith(""), true)
	wl1, wl2 := uuid.New(), uuid.New()
	repo.links = []domain.Link{{WorkloadID: wl1, Branch: "main", RepoFullName: "old"}, {WorkloadID: wl2, Branch: "main"}}
	ctx := context.Background()

	status, err := s.HandleWebhook(ctx, "d1", "push", pushBody(t, nil))
	require.NoError(t, err)
	assert.Equal(t, WebhookAccepted, status)
	require.Len(t, builds.reqs, 2, "un build par conteneur")
	assert.Equal(t, buildsDomain.TriggerPush, builds.reqs[0].Trigger)
	assert.Equal(t, strings.Repeat("a", 40), builds.reqs[0].Commit)
	assert.True(t, builds.reqs[0].RecordRejection, "quota : trace visible")
	assert.Contains(t, builds.reqs[0].CloneURL, "awa/shop.git", "nom à jour")

	// Rejeu de la même livraison.
	status, err = s.HandleWebhook(ctx, "d1", "push", pushBody(t, nil))
	require.NoError(t, err)
	assert.Equal(t, WebhookDuplicate, status)
	assert.Len(t, builds.reqs, 2)

	ignored := map[string]func(*domain.PushEvent){
		"tag":        func(e *domain.PushEvent) { e.Ref = "refs/tags/v1" },
		"deleted":    func(e *domain.PushEvent) { e.Deleted = true },
		"zero":       func(e *domain.PushEvent) { e.After = strings.Repeat("0", 40) },
		"bad commit": func(e *domain.PushEvent) { e.After = "$(id)" },
		"injection":  func(e *domain.PushEvent) { e.Ref = "refs/heads/$(id)" },
		"no repo":    func(e *domain.PushEvent) { e.Repository.ID = 0 },
	}
	for name, mutate := range ignored {
		status, err := s.HandleWebhook(ctx, "d-"+name, "push", pushBody(t, mutate))
		require.NoError(t, err, name)
		assert.Equal(t, WebhookIgnored, status, name)
	}
	assert.Len(t, builds.reqs, 2)

	repo.links = nil
	status, err = s.HandleWebhook(ctx, "d-other", "push", pushBody(t, func(e *domain.PushEvent) { e.Ref = "refs/heads/dev" }))
	require.NoError(t, err)
	assert.Equal(t, WebhookIgnored, status, "aucun lien sur cette branche")
	assert.Equal(t, "dev", repo.lastBranch)
}

// Quota atteint : 2xx, et le build FAILED reste visible.
func TestWebhook_PushOverQuotaStaysAccepted(t *testing.T) {
	s, repo, builds := newService(t, githubWith(""), true)
	builds.err = buildsDomain.ErrTooManyBuilds
	repo.links = []domain.Link{{WorkloadID: uuid.New(), Branch: "main"}}
	status, err := s.HandleWebhook(context.Background(), "d", "push", pushBody(t, nil))
	require.NoError(t, err)
	assert.Equal(t, WebhookAccepted, status)
}

func TestWebhook_InstallationDeleted(t *testing.T) {
	s, repo, _ := newService(t, githubWith(""), true)
	status, err := s.HandleWebhook(context.Background(), "d", "installation", []byte(`{"action":"deleted","installation":{"id":3}}`))
	require.NoError(t, err)
	assert.Equal(t, WebhookAccepted, status)
	assert.Equal(t, []int64{3}, repo.gone)
}

// Désactivée : tout répond proprement, sans panique.
func TestDisabled(t *testing.T) {
	s := &Service{}
	assert.False(t, s.Enabled())
	v, err := s.GetLink(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.False(t, v.Enabled)
	_, err = s.InstallURL(context.Background(), uuid.New(), uuid.New())
	require.ErrorIs(t, err, domain.ErrDisabled)
	_, err = s.CloneAccess(context.Background(), uuid.New(), 1, 1)
	require.ErrorIs(t, err, buildsDomain.ErrGitAccessLost)
}

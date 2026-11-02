package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
)

type fakeDNS struct {
	txt   map[string][]string
	hosts map[string][]string
}

func (f fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if v, ok := f.txt[name]; ok {
		return v, nil
	}
	return nil, errors.New("no such host")
}
func (f fakeDNS) LookupHost(_ context.Context, host string) ([]string, error) {
	if v, ok := f.hosts[host]; ok {
		return v, nil
	}
	return nil, errors.New("no such host")
}

type fakeDomains struct {
	repository.DomainStore
	rows     map[uuid.UUID]*domain.WorkloadDomain
	siteHost string
}

func (f *fakeDomains) Create(_ context.Context, d *domain.WorkloadDomain) error {
	f.rows[d.ID] = d
	return nil
}
func (f *fakeDomains) List(_ context.Context, w uuid.UUID) ([]domain.WorkloadDomain, error) {
	out := []domain.WorkloadDomain{}
	for _, d := range f.rows {
		if d.WorkloadID == w {
			out = append(out, *d)
		}
	}
	return out, nil
}
func (f *fakeDomains) Get(_ context.Context, w, id uuid.UUID) (*domain.WorkloadDomain, error) {
	if d, ok := f.rows[id]; ok && d.WorkloadID == w {
		c := *d
		return &c, nil
	}
	return nil, domain.ErrDomainNotFound
}
func (f *fakeDomains) Delete(_ context.Context, _, id uuid.UUID) error {
	delete(f.rows, id)
	return nil
}
func (f *fakeDomains) VerifiedHosts(_ context.Context, w uuid.UUID) ([]string, error) {
	out := []string{}
	for _, d := range f.rows {
		if d.WorkloadID == w && d.Verified {
			out = append(out, d.Hostname)
		}
	}
	return out, nil
}
func (f *fakeDomains) VerifiedOwner(_ context.Context, host string, except uuid.UUID) (*domain.WorkloadDomain, error) {
	for _, d := range f.rows {
		if d.Hostname == host && d.Verified && d.ID != except {
			return d, nil
		}
	}
	return nil, nil
}
func (f *fakeDomains) SiteHostnameTaken(_ context.Context, host string) (bool, error) {
	return host == f.siteHost, nil
}
func (f *fakeDomains) SetVerified(_ context.Context, id uuid.UUID, v bool) error {
	f.rows[id].Verified = v
	return nil
}
func (f *fakeDomains) Touch(context.Context, uuid.UUID) error { return nil }

const domainTarget = "203.0.113.10"

func domainService(t *testing.T) (*WorkloadService, *fakeDomains, *fakeDNS, *fakeDistributor, uuid.UUID) {
	t.Helper()
	saved := configs.AppConfig
	t.Cleanup(func() { configs.AppConfig = saved })
	configs.AppConfig.Vps.AdressIp = domainTarget
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Image: "nginx:1", Status: "RUNNING", TargetPort: 8080}}
	dns := &fakeDNS{txt: map[string][]string{}, hosts: map[string][]string{}}
	store := &fakeDomains{rows: map[uuid.UUID]*domain.WorkloadDomain{}}
	dist := &fakeDistributor{}
	return &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}, Domains: store,
		DomainCfg: &DomainSettings{Resolver: dns, Target: domainTarget, TLS: true}}, store, dns, dist, id
}

// Sans la preuve TXT, pointer vers la plateforme ne donne rien : sinon un
// client captait n'importe quel domaine qui nous vise déjà.
func TestVerifyDomain_PointingIsNotProof(t *testing.T) {
	s, store, dns, dist, id := domainService(t)
	d, err := s.AddDomain(context.Background(), id, "WWW.Exemple.SN.")
	require.NoError(t, err)
	assert.Equal(t, "www.exemple.sn", d.Hostname)
	assert.Empty(t, dist.started, "ajouter ne route rien")
	require.Len(t, d.Records, 2)
	assert.Equal(t, domain.DNSRecord{Type: "TXT", Name: "_betorun-challenge.www.exemple.sn", Value: store.rows[d.ID].Token}, d.Records[0])
	assert.Equal(t, domain.DNSRecord{Type: "A", Name: "www.exemple.sn", Value: domainTarget}, d.Records[1])

	dns.hosts["www.exemple.sn"] = []string{domainTarget}
	v, err := s.VerifyDomain(context.Background(), id, d.ID)
	require.NoError(t, err)
	assert.False(t, v.Verified)
	assert.True(t, v.Pointing)
	assert.Empty(t, dist.started)

	// Le jeton d'une autre demande ne compte pas.
	dns.txt["_betorun-challenge.www.exemple.sn"] = []string{"betorun-verify=autre"}
	v, err = s.VerifyDomain(context.Background(), id, d.ID)
	require.NoError(t, err)
	assert.False(t, v.Verified)

	dns.txt["_betorun-challenge.www.exemple.sn"] = []string{store.rows[d.ID].Token}
	v, err = s.VerifyDomain(context.Background(), id, d.ID)
	require.NoError(t, err)
	assert.True(t, v.Verified)
	require.Len(t, dist.started, 1, "preuve faite : nouveau déploiement")
	in := dist.args[0].(workflows.DeployWorkloadInput)
	assert.Equal(t, []string{"www.exemple.sn"}, in.CustomDomains)
	assert.True(t, in.CustomDomainTLS)
}

func TestAddDomain_Invalid(t *testing.T) {
	s, _, _, _, id := domainService(t)
	configs.AppConfig.Sites.BaseDomain = "betorun.app"
	for _, bad := range []string{"", "localhost", "10.0.0.1", "*.exemple.sn", "exemple.sn/x", "a b.sn", "x.betorun.app", "-a.sn"} {
		_, err := s.AddDomain(context.Background(), id, bad)
		assert.Error(t, err, bad)
	}
}

func TestAddDomain_Limit(t *testing.T) {
	s, store, _, _, id := domainService(t)
	for i := 0; i < domain.MaxDomainsPerWorkload; i++ {
		u := uuid.New()
		store.rows[u] = &domain.WorkloadDomain{ID: u, WorkloadID: id, Hostname: u.String() + ".sn"}
	}
	_, err := s.AddDomain(context.Background(), id, "www.exemple.sn")
	assert.ErrorIs(t, err, domain.ErrDomainLimit)
}

func TestVerifyDomain_Conflicts(t *testing.T) {
	const host, challenge = "www.exemple.sn", "_betorun-challenge.www.exemple.sn"
	ctx := context.Background()

	// Preuve de l'ancien détenteur toujours présente : refus.
	s, store, dns, dist, id := domainService(t)
	d, err := s.AddDomain(ctx, id, host)
	require.NoError(t, err)
	other := &domain.WorkloadDomain{ID: uuid.New(), WorkloadID: uuid.New(), Hostname: host, Token: "betorun-verify=ancien", Verified: true}
	store.rows[other.ID] = other
	dns.txt[challenge] = []string{store.rows[d.ID].Token, other.Token}
	_, err = s.VerifyDomain(ctx, id, d.ID)
	assert.ErrorIs(t, err, domain.ErrDomainClaimed)
	assert.True(t, other.Verified)
	assert.Empty(t, dist.started)

	// Preuve de l'ancien détenteur disparue : le domaine a changé de mains.
	dns.txt[challenge] = []string{store.rows[d.ID].Token}
	v, err := s.VerifyDomain(ctx, id, d.ID)
	require.NoError(t, err)
	assert.True(t, v.Verified)
	assert.False(t, other.Verified, "route retirée à l'ancien détenteur")

	// Nom pris par un site statique.
	s, store, dns, _, id = domainService(t)
	store.siteHost = host
	d, err = s.AddDomain(ctx, id, host)
	require.NoError(t, err)
	dns.txt[challenge] = []string{store.rows[d.ID].Token}
	_, err = s.VerifyDomain(ctx, id, d.ID)
	assert.ErrorIs(t, err, domain.ErrDomainClaimed)

	// Un domaine d'un autre conteneur : introuvable.
	_, err = s.VerifyDomain(ctx, uuid.New(), d.ID)
	assert.ErrorIs(t, err, domain.ErrDomainNotFound)
}

// Preuve retirée après coup : le domaine reste routé (une erreur DNS passagère
// ne doit pas couper un service), pas de nouveau déploiement.
func TestVerifyDomain_KeepsRouteWhenProofDisappears(t *testing.T) {
	s, store, _, dist, id := domainService(t)
	u := uuid.New()
	store.rows[u] = &domain.WorkloadDomain{ID: u, WorkloadID: id, Hostname: "www.exemple.sn", Token: "t", Verified: true}
	v, err := s.VerifyDomain(context.Background(), id, u)
	require.NoError(t, err)
	assert.True(t, v.Verified)
	assert.Empty(t, dist.started)
}

func TestRemoveDomain(t *testing.T) {
	s, store, _, dist, id := domainService(t)
	u := uuid.New()
	store.rows[u] = &domain.WorkloadDomain{ID: u, WorkloadID: id, Hostname: "a.sn"}
	require.NoError(t, s.RemoveDomain(context.Background(), id, u))
	assert.Empty(t, dist.started, "jamais routé : rien à redéployer")

	u = uuid.New()
	store.rows[u] = &domain.WorkloadDomain{ID: u, WorkloadID: id, Hostname: "b.sn", Verified: true}
	require.NoError(t, s.RemoveDomain(context.Background(), id, u))
	assert.Len(t, dist.started, 1, "route à retirer")
	assert.Empty(t, store.rows)
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
	"github.com/thekrauss/kubemanager/internal/modules/sites/repository"
)

type fakeDNS struct {
	cnames map[string]string
	hosts  map[string][]string
	txt    map[string][]string
}

func (f fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if v, ok := f.txt[name]; ok {
		return v, nil
	}
	return nil, errors.New("no txt")
}

func (f fakeDNS) LookupCNAME(_ context.Context, host string) (string, error) {
	if c, ok := f.cnames[host]; ok {
		return c, nil
	}
	return "", errors.New("no cname")
}

func (f fakeDNS) LookupHost(_ context.Context, host string) ([]string, error) {
	if ips, ok := f.hosts[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no host")
}

func newVerifyService(dns fakeDNS) *SiteService {
	return NewSiteService(nil, nil, nil, dns, Config{BaseDomain: "betorun.app", EdgeTarget: "edge.betorun.app"}, nil)
}

func TestPointsToEdge(t *testing.T) {
	dns := fakeDNS{
		cnames: map[string]string{
			"www.shop.sn":   "edge.betorun.app.",
			"blog.shop.sn":  "shop.betorun.app.",
			"other.shop.sn": "someone-else.netlify.app.",
		},
		hosts: map[string][]string{
			"edge.betorun.app": {"203.0.113.10", "203.0.113.11"},
			"shop.sn":          {"203.0.113.10"},                 // apex en A
			"mixed.sn":         {"203.0.113.10", "198.51.100.7"}, // pointe aussi ailleurs
		},
	}
	s := newVerifyService(dns)
	ctx := context.Background()

	assert.True(t, s.pointsToEdge(ctx, "www.shop.sn", "shop.betorun.app"), "CNAME vers l'edge")
	assert.True(t, s.pointsToEdge(ctx, "blog.shop.sn", "shop.betorun.app"), "CNAME vers le sous-domaine du site")
	assert.True(t, s.pointsToEdge(ctx, "shop.sn", "shop.betorun.app"), "apex en A sur les IP de l'edge")

	assert.False(t, s.pointsToEdge(ctx, "other.shop.sn", "shop.betorun.app"), "CNAME ailleurs")
	assert.False(t, s.pointsToEdge(ctx, "mixed.sn", "shop.betorun.app"), "une IP hors edge suffit à refuser")
	assert.False(t, s.pointsToEdge(ctx, "nxdomain.sn", "shop.betorun.app"), "domaine inexistant")
}

type fakeSiteRepo struct {
	repository.SiteRepository
	site    *domain.Site
	domains map[uuid.UUID]*domain.SiteDomain
}

func (f *fakeSiteRepo) GetSite(context.Context, uuid.UUID, uuid.UUID) (*domain.Site, error) {
	return f.site, nil
}
func (f *fakeSiteRepo) GetDomain(_ context.Context, _, id uuid.UUID) (*domain.SiteDomain, error) {
	c := *f.domains[id]
	return &c, nil
}
func (f *fakeSiteRepo) TouchDomain(context.Context, uuid.UUID, time.Time) error { return nil }
func (f *fakeSiteRepo) VerifiedDomain(_ context.Context, host string, except uuid.UUID) (*domain.SiteDomain, error) {
	for _, d := range f.domains {
		if d.Hostname == host && d.Verified && d.ID != except {
			return d, nil
		}
	}
	return nil, nil
}
func (f *fakeSiteRepo) SetDomainVerification(_ context.Context, id uuid.UUID, v bool, _ time.Time) error {
	f.domains[id].Verified = v
	return nil
}

func verifySetup(dns fakeDNS, ds ...*domain.SiteDomain) (*SiteService, *fakeSiteRepo) {
	repo := &fakeSiteRepo{site: &domain.Site{ID: uuid.New(), Subdomain: "shop", Status: domain.SiteStatusLive}, domains: map[uuid.UUID]*domain.SiteDomain{}}
	for _, d := range ds {
		repo.domains[d.ID] = d
	}
	return NewSiteService(repo, nil, nil, dns, Config{BaseDomain: "betorun.app", EdgeTarget: "edge.betorun.app"}, nil), repo
}

// Pointer vers la plateforme ne prouve rien : c'était la faille de captation.
func TestVerifyDomain_PointingIsNotProof(t *testing.T) {
	const host, ch = "www.shop.sn", "_betorun-challenge.www.shop.sn"
	mine := &domain.SiteDomain{ID: uuid.New(), Hostname: host, Token: "betorun-verify=mine"}
	dns := fakeDNS{cnames: map[string]string{host: "edge.betorun.app."}, txt: map[string][]string{}}
	s, repo := verifySetup(dns, mine)
	ctx := context.Background()

	v, err := s.VerifyDomain(ctx, uuid.New(), repo.site.ID, mine.ID)
	require.NoError(t, err)
	assert.True(t, v.Pointing)
	assert.False(t, v.Verified, "CNAME sans preuve : refusé")
	require.Len(t, v.Records, 2)
	assert.Equal(t, domain.DNSRecord{Type: "TXT", Name: ch, Value: "betorun-verify=mine"}, v.Records[0])

	dns.txt[ch] = []string{"betorun-verify=autre"}
	v, err = s.VerifyDomain(ctx, uuid.New(), repo.site.ID, mine.ID)
	require.NoError(t, err)
	assert.False(t, v.Verified)

	dns.txt[ch] = []string{"betorun-verify=mine"}
	v, err = s.VerifyDomain(ctx, uuid.New(), repo.site.ID, mine.ID)
	require.NoError(t, err)
	assert.True(t, v.Verified)
}

func TestVerifyDomain_Ownership(t *testing.T) {
	const host, ch = "www.shop.sn", "_betorun-challenge.www.shop.sn"
	ctx := context.Background()
	mine := func() *domain.SiteDomain {
		return &domain.SiteDomain{ID: uuid.New(), Hostname: host, Token: "betorun-verify=mine"}
	}

	// Les deux preuves présentes : le détenteur garde le domaine.
	old := &domain.SiteDomain{ID: uuid.New(), Hostname: host, Token: "betorun-verify=old", Verified: true}
	m := mine()
	s, repo := verifySetup(fakeDNS{txt: map[string][]string{ch: {"betorun-verify=mine", "betorun-verify=old"}}}, old, m)
	_, err := s.VerifyDomain(ctx, uuid.New(), repo.site.ID, m.ID)
	assert.Error(t, err)
	assert.True(t, old.Verified)
	assert.False(t, m.Verified)

	// Seule la nouvelle preuve : le domaine a changé de mains.
	old = &domain.SiteDomain{ID: uuid.New(), Hostname: host, Token: "betorun-verify=old", Verified: true}
	m = mine()
	s, repo = verifySetup(fakeDNS{txt: map[string][]string{ch: {"betorun-verify=mine"}}}, old, m)
	v, err := s.VerifyDomain(ctx, uuid.New(), repo.site.ID, m.ID)
	require.NoError(t, err)
	assert.True(t, v.Verified)
	assert.False(t, old.Verified)

	// Domaine vérifié avant la preuve (sans jeton) : cède devant une preuve.
	legacy := &domain.SiteDomain{ID: uuid.New(), Hostname: host, Verified: true}
	m = mine()
	s, repo = verifySetup(fakeDNS{txt: map[string][]string{ch: {"betorun-verify=mine"}}}, legacy, m)
	_, err = s.VerifyDomain(ctx, uuid.New(), repo.site.ID, m.ID)
	require.NoError(t, err)
	assert.False(t, legacy.Verified)

	// … mais reste servi sinon, et une preuve absente ne coupe rien.
	legacy = &domain.SiteDomain{ID: uuid.New(), Hostname: host, Verified: true}
	s, repo = verifySetup(fakeDNS{txt: map[string][]string{}}, legacy)
	v, err = s.VerifyDomain(ctx, uuid.New(), repo.site.ID, legacy.ID)
	require.NoError(t, err)
	assert.True(t, v.Verified)

	// Nom pris par un conteneur.
	m = mine()
	s, repo = verifySetup(fakeDNS{txt: map[string][]string{ch: {"betorun-verify=mine"}}}, m)
	s.HostnameInUse = func(context.Context, string) (bool, error) { return true, nil }
	_, err = s.VerifyDomain(ctx, uuid.New(), repo.site.ID, m.ID)
	assert.Error(t, err)
	assert.False(t, m.Verified)
}

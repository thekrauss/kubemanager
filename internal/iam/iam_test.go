package iam

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Catalogue et rôles ───────────────────────────────────────────────────

func TestRoles_OnlyKnownPermissions(t *testing.T) {
	for _, r := range predefinedRoles {
		assert.True(t, IsPredefinedRoleName(r.Name), r.Name)
		for _, p := range r.Permissions {
			info, ok := Info(p)
			require.True(t, ok, "%s references unknown permission %s", r.Name, p)
			assert.Equal(t, r.PlatformOnly, info.PlatformOnly,
				"%s: platform permissions only in platform roles (%s)", r.Name, p)
		}
	}
}

func TestRoles_BasicRolesAreNested(t *testing.T) {
	chain := []string{"roles/viewer", "roles/developer", "roles/editor", "roles/owner"}
	for i := 1; i < len(chain); i++ {
		lower, _ := LookupRole(chain[i-1])
		upper, _ := LookupRole(chain[i])
		for _, p := range lower.Permissions {
			assert.True(t, upper.has(p), "%s must include %s from %s", chain[i], p, chain[i-1])
		}
	}
}

func TestRoles_DeveloperParityWithLegacyRole(t *testing.T) {
	dev, _ := LookupRole("roles/developer")
	for _, p := range []Permission{WorkloadsDelete, SitesDelete, ProjectsDelete, ProjectsSetIamPolicy} {
		assert.False(t, dev.has(p), "roles/developer must not grant %s (legacy DEVELOPER could not)", p)
	}
}

func TestRoles_OwnerHasEveryCustomerPermission(t *testing.T) {
	owner, _ := LookupRole("roles/owner")
	for _, p := range AllPermissions() {
		info, _ := Info(p)
		if !info.PlatformOnly {
			assert.True(t, owner.has(p), "roles/owner must grant %s", p)
		}
	}
}

func TestPredefinedRoles_HidesPlatformRoles(t *testing.T) {
	for _, r := range PredefinedRoles() {
		assert.False(t, r.PlatformOnly, r.Name)
	}
}

// ── Noms et principals ───────────────────────────────────────────────────

func TestResourceName_Validate(t *testing.T) {
	p, s := uuid.New(), uuid.New()
	valid := []ResourceName{Platform, ProjectName(p), ProjectName(p).Child("sites", s),
		ProjectName(p).Child("serviceAccounts", s), OrganizationName(p)}
	for _, n := range valid {
		assert.NoError(t, n.Validate(), n)
	}
	invalid := []ResourceName{"", "projects", "projects/not-a-uuid", "workloads/" + ResourceName(p.String()),
		ResourceName("projects/" + p.String() + "/sites"), ResourceName("projects/" + p.String() + "/si-tes/" + s.String())}
	for _, n := range invalid {
		assert.ErrorIs(t, n.Validate(), ErrInvalidResourceName, n)
	}
}

func TestPrincipal_Parse(t *testing.T) {
	id := uuid.New()
	kind, got, err := UserPrincipal(id).Parse()
	require.NoError(t, err)
	assert.Equal(t, KindUser, kind)
	assert.Equal(t, id, got)
	for _, bad := range []Principal{"", "user:", "user:nope", "robot:" + Principal(id.String()), Principal(id.String())} {
		assert.ErrorIs(t, bad.Validate(), ErrInvalidPrincipal, bad)
	}
	assert.NoError(t, AllUsers.Validate())
}

// ── Engine ───────────────────────────────────────────────────────────────

type memStore struct {
	bindings []Binding
	orgs     map[uuid.UUID]uuid.UUID
	calls    int
}

func (m *memStore) BindingsFor(_ context.Context, ps []Principal) ([]Binding, error) {
	m.calls++
	want := map[Principal]bool{}
	for _, p := range ps {
		want[p] = true
	}
	var out []Binding
	for _, b := range m.bindings {
		if want[b.Principal] {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *memStore) ProjectOrganization(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	org, ok := m.orgs[id]
	if !ok {
		return uuid.Nil, ErrResourceNotFound
	}
	return org, nil
}

type fixture struct {
	org, project, otherProject uuid.UUID
	alice, bob, staff          Principal
	store                      *memStore
	engine                     *Engine
}

func newFixture() *fixture {
	f := &fixture{org: uuid.New(), project: uuid.New(), otherProject: uuid.New(),
		alice: UserPrincipal(uuid.New()), bob: UserPrincipal(uuid.New()), staff: UserPrincipal(uuid.New())}
	f.store = &memStore{orgs: map[uuid.UUID]uuid.UUID{f.project: f.org, f.otherProject: uuid.New()}}
	f.engine = NewEngine(f.store, time.Minute)
	return f
}

func (f *fixture) grant(r ResourceName, role string, p Principal) {
	f.store.bindings = append(f.store.bindings, Binding{Resource: r, Role: role, Principal: p})
	f.engine.Invalidate(p)
}

func (f *fixture) allowed(t *testing.T, p Principal, perm Permission, r ResourceName) bool {
	t.Helper()
	d, err := f.engine.Check(context.Background(), p, perm, r)
	require.NoError(t, err)
	return d.Allowed
}

func TestEngine_DenyByDefault(t *testing.T) {
	f := newFixture()
	assert.False(t, f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project)))
}

func TestEngine_InheritsFromProjectAndOrganization(t *testing.T) {
	f := newFixture()
	site := ProjectName(f.project).Child("sites", uuid.New())

	f.grant(ProjectName(f.project), "roles/viewer", f.alice)
	assert.True(t, f.allowed(t, f.alice, SitesGet, site), "projet → site")
	assert.False(t, f.allowed(t, f.alice, SitesDelete, site))
	assert.False(t, f.allowed(t, f.alice, SitesGet, ProjectName(f.otherProject).Child("sites", uuid.New())),
		"un autre projet n'hérite pas")

	f.grant(OrganizationName(f.org), "roles/editor", f.bob)
	d, err := f.engine.Check(context.Background(), f.bob, SitesDelete, site)
	require.NoError(t, err)
	assert.True(t, d.Allowed, "organisation → projet → site")
	assert.Equal(t, OrganizationName(f.org), d.Resource)
	assert.Equal(t, "roles/editor", d.Role)
}

func TestEngine_ResourceLevelBinding(t *testing.T) {
	f := newFixture()
	siteA := ProjectName(f.project).Child("sites", uuid.New())
	siteB := ProjectName(f.project).Child("sites", uuid.New())
	f.grant(siteA, "roles/sites.deployer", f.alice)
	assert.True(t, f.allowed(t, f.alice, SiteDeploymentsCreate, siteA))
	assert.False(t, f.allowed(t, f.alice, SiteDeploymentsCreate, siteB), "binding limité à un site")
	assert.False(t, f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project)), "pas d'héritage vers le haut")
}

func TestEngine_ConditionExpiry(t *testing.T) {
	f := newFixture()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	f.engine.now = func() time.Time { return now }
	f.store.bindings = append(f.store.bindings, Binding{
		Resource: ProjectName(f.project), Role: "roles/viewer", Principal: f.alice,
		Condition: &Condition{Title: "temp", Expression: "request.time < timestamp('2026-11-01T00:00:00Z')"},
	}, Binding{
		Resource: ProjectName(f.project), Role: "roles/owner", Principal: f.alice,
		Condition: &Condition{Title: "bogus", Expression: "resource.name.startsWith('x')"},
	})

	assert.True(t, f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project)))
	assert.False(t, f.allowed(t, f.alice, ProjectsDelete, ProjectName(f.project)), "condition non supportée = refus")

	now = now.Add(5 * 24 * time.Hour)
	f.engine.Invalidate(f.alice)
	assert.False(t, f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project)), "binding expiré")
}

func TestEngine_PlatformRolesOnlyOnPlatform(t *testing.T) {
	f := newFixture()
	f.grant(OrganizationName(f.org), "roles/platform.admin", f.alice) // mauvais endroit
	assert.False(t, f.allowed(t, f.alice, PlatformClustersList, Platform))

	f.grant(Platform, "roles/platform.support", f.staff)
	assert.True(t, f.allowed(t, f.staff, PlatformClustersList, Platform))
	assert.False(t, f.allowed(t, f.staff, PlatformClusterKeysCreate, Platform))
	assert.False(t, f.allowed(t, f.staff, ProjectsGet, ProjectName(f.project)),
		"le staff n'a aucun accès implicite aux projets clients")

	f.grant(Platform, "roles/owner", f.alice)
	assert.False(t, f.allowed(t, f.alice, PlatformClustersList, Platform),
		"une permission plateforme ne s'obtient que par un rôle plateforme")
}

func TestEngine_AllAuthenticatedUsers(t *testing.T) {
	f := newFixture()
	site := ProjectName(f.project).Child("sites", uuid.New())
	f.grant(site, "roles/viewer", AllAuthenticatedUsers)
	assert.True(t, f.allowed(t, f.bob, SitesGet, site))
}

func TestEngine_TestPermissions(t *testing.T) {
	f := newFixture()
	f.grant(ProjectName(f.project), "roles/developer", f.alice)
	got, err := f.engine.TestPermissions(context.Background(), f.alice, ProjectName(f.project),
		[]Permission{WorkloadsCreate, WorkloadsDelete, ProjectsGet})
	require.NoError(t, err)
	assert.Equal(t, []Permission{WorkloadsCreate, ProjectsGet}, got)
}

func TestEngine_Errors(t *testing.T) {
	f := newFixture()
	_, err := f.engine.Check(context.Background(), f.alice, "nope.nope.nope", ProjectName(f.project))
	assert.Error(t, err, "permission inconnue = erreur de programmation")

	_, err = f.engine.Check(context.Background(), f.alice, ProjectsGet, ProjectName(uuid.New()))
	assert.ErrorIs(t, err, ErrResourceNotFound, "projet inconnu")

	_, err = f.engine.Check(context.Background(), "nobody", ProjectsGet, ProjectName(f.project))
	assert.ErrorIs(t, err, ErrInvalidPrincipal)
}

func TestEngine_CacheAndInvalidate(t *testing.T) {
	f := newFixture()
	f.grant(ProjectName(f.project), "roles/viewer", f.alice)
	for i := 0; i < 3; i++ {
		f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project))
	}
	assert.Equal(t, 1, f.store.calls, "bindings mis en cache")

	f.store.bindings = nil // retrait du droit
	f.engine.Invalidate(f.alice)
	assert.False(t, f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project)))
}

func TestEngine_FreshGrantVisibleImmediately(t *testing.T) {
	f := newFixture()
	assert.False(t, f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project))) // cache rempli

	// Droit accordé par un autre process (worker) : aucune invalidation locale.
	f.store.bindings = append(f.store.bindings, Binding{Resource: ProjectName(f.project), Role: "roles/owner", Principal: f.alice})
	assert.True(t, f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project)), "un refus relit toujours la base")

	calls := f.store.calls
	assert.True(t, f.allowed(t, f.alice, ProjectsGet, ProjectName(f.project)))
	assert.Equal(t, calls, f.store.calls, "les accès autorisés restent servis par le cache")
}

func TestValidateCondition(t *testing.T) {
	assert.NoError(t, ValidateCondition(nil))
	assert.NoError(t, ValidateCondition(&Condition{Title: "t", Expression: "request.time < timestamp('2027-01-01T00:00:00Z')"}))
	assert.Error(t, ValidateCondition(&Condition{Expression: "request.time < timestamp('2027-01-01T00:00:00Z')"}), "titre requis")
	assert.ErrorIs(t, ValidateCondition(&Condition{Title: "t", Expression: "true"}), ErrUnsupportedCondition)
}

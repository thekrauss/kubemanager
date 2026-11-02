package iam_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/iam"
)

// Rôles personnalisés : création limitée aux permissions détenues, portée,
// modification et suppression à effet immédiat.
func TestE2E_CustomRoles(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, 0)
	policies := &iam.PolicyService{DB: db, Engine: engine}
	roles := &iam.CustomRoleService{DB: db, Engine: engine}

	awa := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	projectID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	project := iam.ProjectName(projectID)
	site := project.Child("sites", uuid.New())
	dev := newUser(t, db, "dev@ex.sn")
	devP := iam.UserPrincipal(dev)
	roleID := "deployer" + uuid.NewString()[:6]

	// ── Création ──
	_, err := roles.Create(ctx, awa, project, "x", iam.CustomRoleInput{Title: "t", Permissions: []string{"sites.sites.list"}})
	assert.ErrorIs(t, err, iam.ErrInvalidRoleID)
	_, err = roles.Create(ctx, awa, project, roleID, iam.CustomRoleInput{Title: "t", Permissions: []string{"platform.clusters.list"}})
	assert.ErrorIs(t, err, iam.ErrInvalidRolePermissions, "permission staff")
	_, err = roles.Create(ctx, awa, project, roleID, iam.CustomRoleInput{Title: "t"})
	assert.ErrorIs(t, err, iam.ErrCustomRolePermissionCount)
	_, err = roles.Create(ctx, awa, iam.Platform, roleID, iam.CustomRoleInput{Title: "t", Permissions: []string{"sites.sites.list"}})
	assert.ErrorIs(t, err, iam.ErrCustomRoleParent)
	_, err = roles.Create(ctx, devP, project, roleID, iam.CustomRoleInput{Title: "t", Permissions: []string{"sites.sites.list"}})
	assert.ErrorIs(t, err, iam.ErrCustomRoleEscalation, "on ne met pas dans un rôle ce qu'on n'a pas")

	role, err := roles.Create(ctx, awa, project, roleID, iam.CustomRoleInput{Title: "Déployeur CI",
		Permissions: []string{"sites.deployments.create", "sites.sites.list", "sites.sites.list"}})
	require.NoError(t, err)
	assert.Equal(t, string(project)+"/roles/"+roleID, role.Name)
	assert.Equal(t, []string{"sites.deployments.create", "sites.sites.list"}, role.Permissions, "triées, sans doublon")
	_, err = roles.Create(ctx, awa, project, roleID, iam.CustomRoleInput{Title: "t", Permissions: []string{"sites.sites.list"}})
	assert.ErrorIs(t, err, iam.ErrRoleIDTaken)

	// ── Attribution ──
	grant := func(roleName string, member iam.Principal) error {
		view, err := policies.Get(ctx, project)
		require.NoError(t, err)
		next := view.Policy
		next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: roleName, Members: []iam.Principal{member}})
		_, err = policies.Set(ctx, awa, project, next)
		return err
	}
	assert.ErrorIs(t, grant("projects/"+uuid.NewString()+"/roles/nope", devP), iam.ErrRoleNotFound)
	otherProject := iam.ProjectName(uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"))
	otherRole := string(otherProject) + "/roles/" + roleID
	require.NoError(t, db.Exec(`INSERT INTO iam_custom_roles (name, parent_resource, role_id, title, permissions, etag)
		VALUES (?, ?, ?, 't', '{sites.sites.list}', 'e') ON CONFLICT DO NOTHING`, otherRole, string(otherProject), roleID).Error)
	assert.ErrorIs(t, grant(otherRole, devP), iam.ErrCustomRoleOutOfScope, "rôle d'un autre projet")
	require.NoError(t, grant(role.Name, devP))
	assert.True(t, allowed(t, engine, dev, iam.SiteDeploymentsCreate, site))
	assert.False(t, allowed(t, engine, dev, iam.SitesDelete, site))

	// ── Modification : etag, anti-escalade, effet immédiat ──
	_, err = roles.Update(ctx, awa, role.Name, "stale", iam.CustomRoleInput{Title: "t", Permissions: []string{"sites.sites.list"}})
	assert.ErrorIs(t, err, iam.ErrRoleEtagMismatch)
	_, err = roles.Update(ctx, devP, role.Name, role.Etag, iam.CustomRoleInput{Title: "t",
		Permissions: []string{"sites.deployments.create", "sites.sites.list", "sites.sites.delete"}})
	assert.ErrorIs(t, err, iam.ErrCustomRoleEscalation, "se donner sites.delete via son propre rôle")
	updated, err := roles.Update(ctx, awa, role.Name, role.Etag, iam.CustomRoleInput{Title: "Lecture", Permissions: []string{"sites.sites.list"}})
	require.NoError(t, err)
	assert.NotEqual(t, role.Etag, updated.Etag)
	assert.False(t, allowed(t, engine, dev, iam.SiteDeploymentsCreate, site), "permission retirée : effet immédiat")
	assert.True(t, allowed(t, engine, dev, iam.SitesList, site))

	list, err := roles.List(ctx, project)
	require.NoError(t, err)
	assert.NotEmpty(t, list)

	// ── Suppression : bindings retirés, plus aucun droit ──
	require.NoError(t, roles.Delete(ctx, awa, role.Name))
	assert.False(t, allowed(t, engine, dev, iam.SitesList, site))
	view, err := policies.Get(ctx, project)
	require.NoError(t, err)
	for _, b := range view.Policy.Bindings {
		assert.NotEqual(t, role.Name, b.Role)
	}
	assert.ErrorIs(t, roles.Delete(ctx, awa, role.Name), iam.ErrRoleNotFound)
}

// Groupes : un rôle accordé au groupe vaut pour ses membres ; ajout de membre
// soumis à l'anti-escalade ; retrait et suppression à effet immédiat.
func TestE2E_Groups(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, 0)
	policies := &iam.PolicyService{DB: db, Engine: engine}
	groups := &iam.GroupService{DB: db, Engine: engine}

	awa := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	projectID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	project := iam.ProjectName(projectID)
	site := project.Child("sites", uuid.New())
	ancestry, err := engine.Ancestry(ctx, project)
	require.NoError(t, err)
	orgID, ok := ancestry[1].Organization()
	require.True(t, ok)

	_, err = groups.Create(ctx, awa, orgID, "Bad ID", "", "")
	assert.ErrorIs(t, err, iam.ErrInvalidGroupID)
	gid := "devs-" + uuid.NewString()[:6]
	g, err := groups.Create(ctx, awa, orgID, gid, "Équipe dev", "")
	require.NoError(t, err)
	_, err = groups.Create(ctx, awa, orgID, gid, "", "")
	assert.ErrorIs(t, err, iam.ErrGroupIDTaken)

	// Rôle accordé au groupe.
	view, err := policies.Get(ctx, project)
	require.NoError(t, err)
	next := view.Policy
	next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: "roles/sites.deployer", Members: []iam.Principal{g.Principal}})
	view, err = policies.Set(ctx, awa, project, next)
	require.NoError(t, err)
	var described bool
	for _, m := range view.Members {
		described = described || (m.Principal == g.Principal && m.DisplayName == "Équipe dev")
	}
	assert.True(t, described, "membre groupe décrit dans la policy")

	// Un groupe d'une autre organisation n'est pas attribuable ici.
	otherOrg := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, display_name) VALUES (?, 'autre')`, otherOrg).Error)
	foreign, err := groups.Create(ctx, awa, otherOrg, "ext-"+uuid.NewString()[:6], "", "")
	require.NoError(t, err)
	view, _ = policies.Get(ctx, project)
	next = view.Policy
	next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: "roles/viewer", Members: []iam.Principal{foreign.Principal}})
	_, err = policies.Set(ctx, awa, project, next)
	assert.ErrorIs(t, err, iam.ErrGroupOutOfScope)

	// Membres.
	alice := newUser(t, db, "alice@ex.sn")
	bob := newUser(t, db, "bob@ex.sn")
	assert.False(t, allowed(t, engine, alice, iam.SiteDeploymentsCreate, site))
	var aliceEmail string
	require.NoError(t, db.Raw(`SELECT email FROM users WHERE id = ?`, alice).Row().Scan(&aliceEmail))
	m, err := groups.AddMember(ctx, awa, orgID, g.ID, iam.Principal("user:"+aliceEmail))
	require.NoError(t, err)
	assert.Equal(t, iam.UserPrincipal(alice), m.Principal, "email résolu")
	assert.True(t, allowed(t, engine, alice, iam.SiteDeploymentsCreate, site), "droit du groupe, effet immédiat")
	assert.False(t, allowed(t, engine, alice, iam.SitesDelete, site))

	_, err = groups.AddMember(ctx, awa, orgID, g.ID, foreign.Principal)
	assert.ErrorIs(t, err, iam.ErrNestedGroup)
	// Bob n'a aucun droit : il ne peut pas faire entrer quelqu'un dans un groupe qui en a.
	_, err = groups.AddMember(ctx, iam.UserPrincipal(bob), orgID, g.ID, iam.UserPrincipal(bob))
	assert.ErrorIs(t, err, iam.ErrGroupEscalation)

	members, err := groups.ListMembers(ctx, orgID, g.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, aliceEmail, members[0].Email)
	list, err := groups.List(ctx, orgID)
	require.NoError(t, err)
	for _, x := range list {
		if x.ID == g.ID {
			assert.Equal(t, 1, x.MemberCount)
		}
	}

	// Retrait, par l'email qui a servi à l'ajouter (avant : 404, accès conservé).
	require.NoError(t, groups.RemoveMember(ctx, orgID, g.ID, iam.Principal("user:"+aliceEmail)))
	assert.False(t, allowed(t, engine, alice, iam.SiteDeploymentsCreate, site))
	assert.ErrorIs(t, groups.RemoveMember(ctx, orgID, g.ID, iam.UserPrincipal(alice)), iam.ErrGroupMemberNotFound)

	// Suppression : rôles du groupe retirés partout.
	_, err = groups.AddMember(ctx, awa, orgID, g.ID, iam.UserPrincipal(alice))
	require.NoError(t, err)
	require.NoError(t, groups.Delete(ctx, awa, orgID, g.ID))
	assert.False(t, allowed(t, engine, alice, iam.SiteDeploymentsCreate, site))
	view, _ = policies.Get(ctx, project)
	for _, b := range view.Policy.Bindings {
		assert.NotContains(t, b.Members, g.Principal)
	}
	_, err = groups.Get(ctx, orgID, g.ID)
	assert.ErrorIs(t, err, iam.ErrGroupNotFound)
}

// iam.requireMfa : posé sur l'organisation, vaut pour ses projets.
func TestE2E_OrgRequireMFA(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, 0)
	awa := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	project := iam.ProjectName(uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))
	ancestry, err := engine.Ancestry(ctx, project)
	require.NoError(t, err)
	orgID, _ := ancestry[1].Organization()

	off := iam.NewOrgPolicyService(db, engine, false)
	_, err = off.SetRequireMFA(ctx, awa, orgID, true)
	assert.ErrorIs(t, err, iam.ErrMFANotConfigured, "pas d'exigence sans MFA vérifiable")

	svc := iam.NewOrgPolicyService(db, engine, true)
	p, err := svc.SetRequireMFA(ctx, awa, orgID, true)
	require.NoError(t, err)
	assert.True(t, p.RequireMFA)
	req, err := svc.RequiresMFA(ctx, project.Child("sites", uuid.New()))
	require.NoError(t, err)
	assert.True(t, req, "hérité par les ressources des projets")
	other, err := svc.RequiresMFA(ctx, iam.ProjectName(uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")))
	require.NoError(t, err)
	_ = other // autre organisation : dépend de la base, seul l'héritage compte ici

	_, err = svc.SetRequireMFA(ctx, awa, orgID, false)
	require.NoError(t, err)
	req, err = svc.RequiresMFA(ctx, project)
	require.NoError(t, err)
	assert.False(t, req, "désactivation immédiate sur ce réplica")
}

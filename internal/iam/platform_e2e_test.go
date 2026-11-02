package iam_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/iam"
)

func TestE2E_PlatformRoles(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, iam.DefaultCacheTTL)

	staff := newUser(t, db, "ops@betorun.app")
	var email string
	require.NoError(t, db.Raw(`SELECT email FROM users WHERE id = ?`, staff).Row().Scan(&email))
	client := iam.ProjectName(uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	desired := []iam.PlatformRoleAssignment{
		{Role: "roles/platform.admin", Emails: []string{" " + email + " ", "nobody-yet@betorun.app"}},
		{Role: "roles/platform.support"},
	}
	res, err := iam.ReconcilePlatformRoles(ctx, db, desired, "system:config")
	require.NoError(t, err)
	assert.Equal(t, []string{"roles/platform.admin " + string(iam.UserPrincipal(staff))}, res.Added)
	assert.Equal(t, []string{"nobody-yet@betorun.app"}, res.UnknownEmails, "signalé, pas une erreur")

	assert.True(t, allowed(t, engine, staff, iam.PlatformClusterKeysCreate, iam.Platform))
	assert.False(t, allowed(t, engine, staff, iam.ProjectsGet, client), "aucun accès implicite aux projets clients")

	res, err = iam.ReconcilePlatformRoles(ctx, db, desired, "system:config")
	require.NoError(t, err)
	assert.Empty(t, res.Added, "idempotent")
	assert.Empty(t, res.Removed)

	// Retiré de la configuration → retiré de la plateforme.
	res, err = iam.ReconcilePlatformRoles(ctx, db, []iam.PlatformRoleAssignment{{Role: "roles/platform.admin"}}, "system:config")
	require.NoError(t, err)
	assert.Equal(t, []string{"roles/platform.admin " + string(iam.UserPrincipal(staff))}, res.Removed)
	engine.Invalidate(iam.UserPrincipal(staff))
	assert.False(t, allowed(t, engine, staff, iam.PlatformClustersList, iam.Platform))

	_, err = iam.ReconcilePlatformRoles(ctx, db, []iam.PlatformRoleAssignment{{Role: "roles/owner"}}, "system:config")
	assert.Error(t, err, "seuls les rôles plateforme sont réconciliables")
}

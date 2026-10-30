// Test d'intégration : vrai Postgres, ignoré sans IAM_E2E.
//
//	bash scripts/iam_e2e_db.sh    # base neuve : migrations + données legacy
//	IAM_E2E=1 go test -p 1 ./internal/iam/... -run TestE2E -v
//
// Les tests ajoutent des données : recréer la base avant chaque série.
//
// -p 1 : les paquets iam et iam/sts modifient la policy du même projet de
// test ; en parallèle, l'etag refuse (à raison) l'une des écritures.
package iam_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/thekrauss/kubemanager/internal/iam"
	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	authrepo "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"github.com/thekrauss/kubemanager/internal/modules/organizations"
	projectrepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
)

const e2eDSN = "postgres://postgres:pw@127.0.0.1:55433/postgres?sslmode=disable"

func e2eDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("IAM_E2E") == "" {
		t.Skip("IAM_E2E not set")
	}
	db, err := gorm.Open(postgres.Open(e2eDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	return db
}

func newUser(t *testing.T, db *gorm.DB, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email = id.String()[:8] + "-" + email // rejouable sur la même base
	require.NoError(t, db.Exec(`INSERT INTO users (id, keycloak_user_id, email, full_name) VALUES (?, ?, ?, ?)`,
		id, id.String(), email, email).Error)
	return id
}

func allowed(t *testing.T, e *iam.Engine, user uuid.UUID, perm iam.Permission, r iam.ResourceName) bool {
	t.Helper()
	d, err := e.Check(context.Background(), iam.UserPrincipal(user), perm, r)
	require.NoError(t, err)
	return d.Allowed
}

func TestE2E_IAM(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, iam.DefaultCacheTTL)

	// ── Données reprises par la migration 000003 ──
	awa := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	moussa := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	fatou := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	sunupay := iam.ProjectName(uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	assert.True(t, allowed(t, engine, awa, iam.ProjectsDelete, sunupay), "OWNER → roles/owner")
	assert.True(t, allowed(t, engine, moussa, iam.WorkloadsCreate, sunupay), "DEVELOPER → roles/developer")
	assert.False(t, allowed(t, engine, moussa, iam.WorkloadsDelete, sunupay), "parité : DEVELOPER ne supprimait pas")
	assert.False(t, allowed(t, engine, fatou, iam.ProjectsGet, sunupay), "invitation PENDING : aucun droit")

	// ── Héritage par l'organisation ──
	var awaOrg uuid.UUID
	require.NoError(t, db.Raw(`SELECT organization_id FROM projects WHERE id = ?`, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa").Row().Scan(&awaOrg))
	require.NoError(t, iam.AddBinding(ctx, db, iam.Binding{Resource: iam.OrganizationName(awaOrg), Role: "roles/viewer",
		Principal: iam.UserPrincipal(fatou)}, "test"))
	engine.Invalidate(iam.UserPrincipal(fatou))
	assert.True(t, allowed(t, engine, fatou, iam.SitesGet, sunupay.Child("sites", uuid.New())), "org → projet → site")
	require.NoError(t, iam.RemovePrincipal(ctx, db, iam.OrganizationName(awaOrg), iam.UserPrincipal(fatou), "test"))
	engine.Invalidate(iam.UserPrincipal(fatou))
	assert.False(t, allowed(t, engine, fatou, iam.SitesGet, sunupay))

	// ── Création d'un projet : organisation personnelle + owner, effectif tout de suite ──
	newcomer := newUser(t, db, "kofi@ex.gh")
	assert.False(t, allowed(t, engine, newcomer, iam.ProjectsGet, sunupay)) // remplit le cache du principal
	project := &authdomain.Project{Name: "kofi-shop-" + newcomer.String()[:6]}
	require.NoError(t, projectrepo.NewProjectRepository(db).CreateProject(ctx, project, newcomer.String()))
	assert.NotEqual(t, uuid.Nil, project.OrganizationID, "projet rattaché à l'org personnelle créée à la volée")
	assert.True(t, allowed(t, engine, newcomer, iam.ProjectsDelete, iam.ProjectName(project.ID)),
		"le créateur est owner sans attendre l'expiration du cache (pas d'Invalidate ici)")
	assert.True(t, allowed(t, engine, newcomer, iam.ProjectsSetIamPolicy, iam.OrganizationName(project.OrganizationID)),
		"owner de son organisation personnelle")

	// ── Synchronisation project_members → bindings ──
	auth := authrepo.NewAuthRepository(db)
	pn := iam.ProjectName(project.ID)

	require.NoError(t, auth.AddProjectMember(ctx, &authdomain.ProjectMember{ProjectID: project.ID, UserID: moussa, Role: authdomain.Role{Name: "VIEWER"}}))
	engine.Invalidate(iam.UserPrincipal(moussa))
	assert.True(t, allowed(t, engine, moussa, iam.WorkloadsGet, pn))
	assert.False(t, allowed(t, engine, moussa, iam.WorkloadsCreate, pn))

	require.NoError(t, auth.Upsert(ctx, &authdomain.ProjectMember{ProjectID: project.ID, UserID: moussa, Role: authdomain.Role{Name: "DEVELOPER"},
		Status: authdomain.ProjectMemberStatusActive}))
	engine.Invalidate(iam.UserPrincipal(moussa))
	assert.True(t, allowed(t, engine, moussa, iam.WorkloadsCreate, pn), "le nouveau rôle remplace l'ancien")
	var n int64
	db.Raw(`SELECT count(*) FROM iam_bindings WHERE resource_name = ? AND principal = ?`, pn.String(), iam.UserPrincipal(moussa).String()).Scan(&n)
	assert.Equal(t, int64(1), n, "un seul binding pour le membre")

	// Vue « membres » calculée depuis les bindings (project_members n'existe plus).
	members, err := auth.ListProjectMembers(ctx, project.ID)
	require.NoError(t, err)
	byUser := map[uuid.UUID]authdomain.ProjectMember{}
	for _, m := range members {
		byUser[m.UserID] = m
	}
	assert.Equal(t, "OWNER", byUser[newcomer].Role.Name)
	assert.Equal(t, "DEVELOPER", byUser[moussa].Role.Name)
	assert.NotEmpty(t, byUser[moussa].User.Email, "infos utilisateur jointes pour l'affichage")
	first, err := auth.FindFirstActiveByUser(ctx, newcomer)
	require.NoError(t, err)
	assert.Equal(t, project.ID, first.ProjectID, "statut « bootstrappé » d'identity")
	ok, err := auth.CheckProjectPermission(ctx, moussa, project.ID, "k8s:workload:create")
	require.NoError(t, err)
	assert.True(t, ok, "permissions legacy calculées depuis le rôle IAM")

	var etagBefore string
	db.Raw(`SELECT etag FROM iam_policies WHERE resource_name = ?`, pn.String()).Scan(&etagBefore)
	require.NoError(t, auth.RemoveProjectMember(ctx, project.ID, moussa))
	engine.Invalidate(iam.UserPrincipal(moussa))
	assert.False(t, allowed(t, engine, moussa, iam.WorkloadsGet, pn), "membre retiré : plus aucun droit")
	var etagAfter string
	db.Raw(`SELECT etag FROM iam_policies WHERE resource_name = ?`, pn.String()).Scan(&etagAfter)
	assert.NotEqual(t, etagBefore, etagAfter, "toute écriture fait tourner l'etag")

	// ── Organisation personnelle : une seule, même en concurrence ──
	racer := newUser(t, db, "racer@ex.ci")
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, 10)
	errs := make([]error, 10)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = organizations.EnsurePersonal(ctx, db, racer, "Racer")
		}(i)
	}
	wg.Wait()
	for i := range ids {
		require.NoError(t, errs[i])
		assert.Equal(t, ids[0], ids[i])
	}
	db.Raw(`SELECT count(*) FROM organizations WHERE kind = 'PERSONAL' AND owner_user_id = ?`, racer).Scan(&n)
	assert.Equal(t, int64(1), n)

	// ── Validation à l'écriture ──
	err = iam.AddBinding(ctx, db, iam.Binding{Resource: sunupay, Role: "roles/platform.admin", Principal: iam.UserPrincipal(awa)}, "test")
	assert.Error(t, err, "rôle staff refusé hors platform")
	err = iam.AddBinding(ctx, db, iam.Binding{Resource: sunupay, Role: "roles/viewer", Principal: iam.UserPrincipal(awa),
		Condition: &iam.Condition{Title: "x", Expression: "true"}}, "test")
	assert.ErrorIs(t, err, iam.ErrUnsupportedCondition)

	// Condition d'expiration réelle (JSONB aller-retour).
	require.NoError(t, iam.AddBinding(ctx, db, iam.Binding{Resource: sunupay, Role: "roles/viewer", Principal: iam.UserPrincipal(racer),
		Condition: &iam.Condition{Title: "audit", Expression: "request.time < timestamp('" + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + "')"}}, "test"))
	assert.True(t, allowed(t, engine, racer, iam.ProjectsGet, sunupay))
}

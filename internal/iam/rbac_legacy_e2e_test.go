package iam_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/iam"
	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	authrepo "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	authsvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	projectrepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
)

// L'ancien endpoint /rbac/assign-role passe par setIamPolicy : mêmes
// protections que l'API IAM (anti-escalade, dernier owner).
func TestE2E_LegacyRBACGoesThroughPolicies(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, iam.DefaultCacheTTL)
	rbac := authsvc.NewRBACService(authrepo.NewAuthRepository(db), zap.NewNop().Sugar())
	rbac.Policies = &iam.PolicyService{DB: db, Engine: engine}

	owner, admin := newUser(t, db, "o@ex.cm"), newUser(t, db, "a@ex.cm")
	p := &authdomain.Project{Name: "rbac-" + owner.String()[:8]}
	require.NoError(t, projectrepo.NewProjectRepository(db).CreateProject(ctx, p, owner.String()))
	assign := func(caller iam.Principal, user, role string) error {
		return rbac.AssignProjectRole(ctx, caller, &authdomain.AssignRoleRequest{UserID: user, ProjectID: p.ID.String(), RoleName: role})
	}

	require.NoError(t, assign(iam.UserPrincipal(owner), admin.String(), "roles/iam.securityAdmin"))
	err := assign(iam.UserPrincipal(admin), admin.String(), "OWNER")
	assert.ErrorIs(t, err, iam.ErrEscalation, "pas d'escalade par l'ancien endpoint")

	err = rbac.RevokeProjectAccess(ctx, iam.UserPrincipal(owner), p.ID.String(), owner.String())
	assert.ErrorIs(t, err, iam.ErrLastOwner, "le dernier owner ne peut pas être retiré")

	require.NoError(t, assign(iam.UserPrincipal(owner), admin.String(), "VIEWER"), "un rôle legacy remplace le précédent")
	members, err := rbac.ListProjectMembers(ctx, p.ID.String())
	require.NoError(t, err)
	roles := map[string]string{}
	for _, m := range members {
		roles[m.UserID] = m.RoleName
	}
	assert.Equal(t, "VIEWER", roles[admin.String()])
	assert.Equal(t, "OWNER", roles[owner.String()])

	roleList, err := rbac.ListAllRoles(ctx)
	require.NoError(t, err)
	assert.Len(t, roleList, 3, "rôles legacy servis par le code (plus de table roles)")
}

package iam_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	projectrepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
)

func TestE2E_AuditLog(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, iam.DefaultCacheTTL)
	policies := &iam.PolicyService{DB: db, Engine: engine}

	// Projet dédié : n'altère pas l'état migré utilisé par les autres tests.
	awa := newUser(t, db, "owner@ex.sn")
	fatou := newUser(t, db, "member@ex.sn")
	p := &authdomain.Project{Name: "audit-" + awa.String()[:8]}
	require.NoError(t, projectrepo.NewProjectRepository(db).CreateProject(ctx, p, awa.String()))
	projectID := p.ID
	project := iam.ProjectName(projectID)

	gin.SetMode(gin.TestMode)
	mw := security.NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil)
	mw.SetAuthorizer(engine)
	r := gin.New()
	r.Use(func(c *gin.Context) { // authentification simulée
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Set("authMethod", "bearer")
	})
	r.Use(mw.AuditMiddleware(&audit.GormWriter{DB: db}))
	setPolicy := func(c *gin.Context) {
		var body struct {
			Policy iam.Policy `json:"policy"`
		}
		_ = c.ShouldBindJSON(&body)
		caller, _ := security.PrincipalFromContext(c)
		if _, err := policies.Set(c.Request.Context(), caller, project, body.Policy); err != nil {
			c.String(http.StatusConflict, err.Error())
			return
		}
		c.Status(http.StatusOK)
	}
	r.POST("/p/:id/policy", mw.Require(iam.ProjectsSetIamPolicy, security.ProjectParam("id")), setPolicy)
	r.GET("/p/:id", mw.Require(iam.ProjectsGet, security.ProjectParam("id")), func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(method, user, body string) int {
		req := httptest.NewRequest(method, "/p/"+projectID.String()+map[bool]string{true: "/policy", false: ""}[method == http.MethodPost], strings.NewReader(body))
		req.Header.Set("X-Test-User", user)
		req.Header.Set("User-Agent", "betorun-cli/1.0")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	count := func() int64 {
		var n int64
		require.NoError(t, db.Raw(`SELECT count(*) FROM audit_log`).Row().Scan(&n))
		return n
	}

	// Lecture autorisée : pas journalisée.
	before := count()
	assert.Equal(t, http.StatusOK, do(http.MethodGet, awa.String(), ""))
	assert.Equal(t, before, count(), "lecture autorisée non journalisée (journal d'accès aux données : phase C)")

	// Écriture autorisée : journalisée avec le détail du changement.
	view, err := policies.Get(ctx, project)
	require.NoError(t, err)
	next := view.Policy
	next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: "roles/viewer", Members: []iam.Principal{iam.UserPrincipal(fatou)}})
	body, _ := json.Marshal(map[string]any{"policy": next})
	assert.Equal(t, http.StatusOK, do(http.MethodPost, awa.String(), string(body)))

	// Lecture refusée : journalisée.
	stranger := newUser(t, db, "stranger@ex.ng")
	assert.Equal(t, http.StatusForbidden, do(http.MethodGet, stranger.String(), ""))

	entries, err := audit.List(ctx, db, audit.Query{Resource: string(project), ProjectID: &projectID})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 2)
	deny, allow := entries[0], entries[1]

	assert.Equal(t, audit.DecisionDeny, deny.Decision)
	assert.Equal(t, string(iam.UserPrincipal(stranger)), deny.Principal)
	assert.Equal(t, "resourcemanager.projects.get", deny.Permission)
	assert.Equal(t, http.StatusForbidden, deny.Status)

	assert.Equal(t, audit.DecisionAllow, allow.Decision)
	assert.Equal(t, "POST /p/:id/policy", allow.Action)
	assert.Equal(t, "resourcemanager.projects.setIamPolicy", allow.Permission)
	assert.Equal(t, string(project), allow.Resource)
	assert.Equal(t, "betorun-cli/1.0", allow.UserAgent)
	assert.Equal(t, []any{"roles/viewer " + string(iam.UserPrincipal(fatou))}, allow.Metadata["bindings_added"])
	assert.NotContains(t, string(allow.MetadataDB), "policy\"", "pas de corps de requête dans le journal")

	// Lecture par organisation (les entrées du projet y figurent).
	var org uuid.UUID
	require.NoError(t, db.Raw(`SELECT organization_id FROM projects WHERE id = ?`, projectID).Row().Scan(&org))
	byOrg, err := audit.List(ctx, db, audit.Query{OrganizationID: &org})
	require.NoError(t, err)
	assert.Equal(t, entries[0].ID, byOrg[0].ID)

	// Pagination par curseur.
	page, err := audit.List(ctx, db, audit.Query{ProjectID: &projectID, Resource: string(project), Limit: 1})
	require.NoError(t, err)
	require.Len(t, page, 1)
	next2, err := audit.List(ctx, db, audit.Query{ProjectID: &projectID, Resource: string(project), Limit: 1, BeforeID: page[0].ID})
	require.NoError(t, err)
	require.Len(t, next2, 1)
	assert.Less(t, next2[0].ID, page[0].ID)

	// Immuable, même pour le rôle applicatif.
	assert.Error(t, db.Exec(`UPDATE audit_log SET decision = 'ALLOW' WHERE id = ?`, deny.ID).Error)
	assert.Error(t, db.Exec(`DELETE FROM audit_log WHERE id = ?`, deny.ID).Error)
	assert.Error(t, db.Exec(`TRUNCATE audit_log`).Error)
	var decision string
	require.NoError(t, db.Raw(`SELECT decision FROM audit_log WHERE id = ?`, deny.ID).Row().Scan(&decision))
	assert.Equal(t, audit.DecisionDeny, decision)
}

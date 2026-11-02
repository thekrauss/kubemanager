package security

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

// fakeAuthorizer : autorise (principal, permission, ressource) listés.
type fakeAuthorizer struct {
	allowed map[string]bool
	err     error
	last    iam.ResourceName
}

func key(p iam.Principal, perm iam.Permission, r iam.ResourceName) string {
	return string(p) + "|" + string(perm) + "|" + string(r)
}

func (f *fakeAuthorizer) Check(_ context.Context, p iam.Principal, perm iam.Permission, r iam.ResourceName) (iam.Decision, error) {
	f.last = r
	if f.err != nil {
		return iam.Decision{}, f.err
	}
	return iam.Decision{Allowed: f.allowed[key(p, perm, r)]}, nil
}

type guardCase struct {
	user    uuid.UUID
	project uuid.UUID
	authz   *fakeAuthorizer
}

func newGuardCase() guardCase {
	u, p := uuid.New(), uuid.New()
	return guardCase{user: u, project: p, authz: &fakeAuthorizer{allowed: map[string]bool{
		key(iam.UserPrincipal(u), iam.WorkloadsCreate, iam.ProjectName(p)): true,
	}}}
}

// run : POST /x/:id gardée par Require(WorkloadsCreate, resolve) ; le handler renvoie le corps lu.
func (gc guardCase) run(t *testing.T, resolve ResourceResolver, userID, url, body string, roles ...string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	m, _ := newTestManager(t, &fakeAuthRepo{})
	m.SetAuthorizer(gc.authz)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", userID)
		if len(roles) > 0 {
			c.Set("roles", roles)
		}
	})
	r.POST("/x/:id", m.Require(iam.WorkloadsCreate, resolve), func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, string(b))
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, url, strings.NewReader(body)))
	return rec
}

func TestRequire_FromBody_AllowedAndBodyRestored(t *testing.T) {
	gc := newGuardCase()
	body := `{"project_id":"` + gc.project.String() + `","name":"api"}`
	rec := gc.run(t, ProjectJSONBody("project_id"), gc.user.String(), "/x/1", body)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, body, rec.Body.String(), "le handler doit relire le corps intact")
}

func TestRequire_OtherProjectForbidden(t *testing.T) {
	gc := newGuardCase()
	rec := gc.run(t, ProjectJSONBody("project_id"), gc.user.String(), "/x/1", `{"project_id":"`+uuid.NewString()+`"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), `"permission":"workloads.workloads.create"`, "la permission manquante est indiquée")
}

func TestRequire_InvalidReference(t *testing.T) {
	gc := newGuardCase()
	for _, body := range []string{`{}`, `not json`, `{"project_id":42}`, `{"project_id":"nope"}`} {
		rec := gc.run(t, ProjectJSONBody("project_id"), gc.user.String(), "/x/1", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
}

func TestRequire_FromQueryAndParam(t *testing.T) {
	gc := newGuardCase()
	assert.Equal(t, http.StatusOK, gc.run(t, ProjectQuery("project_id"), gc.user.String(), "/x/1?project_id="+gc.project.String(), "").Code)
	assert.Equal(t, http.StatusOK, gc.run(t, ProjectParam("id"), gc.user.String(), "/x/"+gc.project.String(), "").Code)
	assert.Equal(t, http.StatusForbidden, gc.run(t, ProjectParam("id"), gc.user.String(), "/x/"+uuid.NewString(), "").Code)
}

func TestRequire_ProjectChildResource(t *testing.T) {
	gc := newGuardCase()
	gin.SetMode(gin.TestMode)
	m, _ := newTestManager(t, &fakeAuthRepo{})
	m.SetAuthorizer(gc.authz)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", gc.user.String()) })
	r.GET("/p/:id/sites/:siteID", m.Require(iam.SitesGet, ProjectChild("id", "sites", "siteID")), func(c *gin.Context) {
		c.String(http.StatusOK, c.GetString("projectID"))
	})
	site := uuid.New()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p/"+gc.project.String()+"/sites/"+site.String(), nil))
	assert.Equal(t, iam.ProjectName(gc.project).Child("sites", site), gc.authz.last, "la ressource précise est vérifiée")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequire_NotFoundAndEngineErrors(t *testing.T) {
	gc := newGuardCase()
	notFound := func(*gin.Context) (iam.ResourceName, error) { return "", ErrResourceNotFound }
	assert.Equal(t, http.StatusNotFound, gc.run(t, notFound, gc.user.String(), "/x/1", "").Code)

	gc.authz.err = iam.ErrResourceNotFound
	assert.Equal(t, http.StatusNotFound, gc.run(t, ProjectParam("id"), gc.user.String(), "/x/"+gc.project.String(), "").Code)

	gc.authz.err = errors.New("db down")
	assert.Equal(t, http.StatusServiceUnavailable, gc.run(t, ProjectParam("id"), gc.user.String(), "/x/"+gc.project.String(), "").Code,
		"échec fermé : jamais d'accès si l'IAM ne répond pas")
}

func TestRequire_NoAuthorizerFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m, _ := newTestManager(t, &fakeAuthRepo{})
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", uuid.NewString()) })
	r.GET("/x/:id", m.Require(iam.ProjectsGet, ProjectParam("id")), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x/"+uuid.NewString(), nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// Les rôles Keycloak SUPER_ADMIN/ADMIN ne donnent plus rien : le staff passe
// par des bindings sur "platform" (iam.md §10).
func TestRequire_KeycloakAdminRoleGrantsNothing(t *testing.T) {
	gc := newGuardCase()
	rec := gc.run(t, ProjectJSONBody("project_id"), uuid.NewString(), "/x/1", `{"project_id":"`+uuid.NewString()+`"}`, domain.GlobalRoleSuperAdmin)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequire_Unauthenticated(t *testing.T) {
	gc := newGuardCase()
	assert.Equal(t, http.StatusUnauthorized, gc.run(t, ProjectParam("id"), "", "/x/"+gc.project.String(), "").Code)
}

func TestRequire_UnknownPermissionPanicsAtStartup(t *testing.T) {
	m, _ := newTestManager(t, &fakeAuthRepo{})
	require.Panics(t, func() { m.Require("nope.nope.nope", PlatformResource) })
}

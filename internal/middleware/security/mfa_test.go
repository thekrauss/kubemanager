package security

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/iam"
)

type allowAll struct{}

func (allowAll) Check(context.Context, iam.Principal, iam.Permission, iam.ResourceName) (iam.Decision, error) {
	return iam.Decision{Allowed: true}, nil
}

type orgRequires bool

func (o orgRequires) RequiresMFA(context.Context, iam.ResourceName) (bool, error) { return bool(o), nil }

func TestCheckMFA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const project = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	type session struct {
		method   string
		acr      string
		authTime time.Time
	}
	human := func(acr string, age time.Duration) session { return session{"bearer", acr, time.Now().Add(-age)} }

	cases := []struct {
		name      string
		acrValues []string
		orgMFA    bool
		perm      iam.Permission
		s         session
		want      int
		maxAge    bool
	}{
		{"sensible sans MFA", []string{"gold"}, false, iam.ProjectsSetIamPolicy, human("silver", 0), 401, true},
		{"sensible, MFA récent", []string{"gold"}, false, iam.ProjectsSetIamPolicy, human("gold", time.Minute), 200, false},
		{"sensible, MFA trop ancien", []string{"gold"}, false, iam.ProjectsSetIamPolicy, human("gold", time.Hour), 401, true},
		{"ordinaire sans MFA", []string{"gold"}, false, iam.ProjectsGet, human("silver", 0), 200, false},
		{"organisation exige le MFA", []string{"gold"}, true, iam.ProjectsGet, human("silver", 0), 401, false},
		{"organisation exige, MFA ancien suffit", []string{"gold"}, true, iam.ProjectsGet, human("gold", 5*time.Hour), 200, false},
		{"compte de service exempté", []string{"gold"}, true, iam.ProjectsSetIamPolicy, session{method: "apikey_v2"}, 200, false},
		{"MFA non configuré", nil, true, iam.ProjectsSetIamPolicy, human("", 0), 200, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMiddlewareManager(&configs.GlobalConfig{}, nil, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil)
			m.SetAuthorizer(allowAll{})
			m.SetMFA(MFAConfig{ACRValues: tc.acrValues, MaxAge: 10 * time.Minute, Policy: orgRequires(tc.orgMFA)})
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(ctxPrincipal, "user:11111111-1111-1111-1111-111111111111")
				c.Set("authMethod", tc.s.method)
				c.Set(ctxAuthAcr, tc.s.acr)
				c.Set(ctxAuthTime, tc.s.authTime.Unix())
			})
			r.GET("/p/:id", m.Require(tc.perm, ProjectParam("id")), func(c *gin.Context) { c.Status(200) })
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", "/p/"+project, nil))
			if rec.Code != tc.want {
				t.Fatalf("status %d, attendu %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == 401 {
				h := rec.Header().Get("WWW-Authenticate")
				if !strings.Contains(h, `error="insufficient_user_authentication"`) || !strings.Contains(h, `acr_values="gold"`) {
					t.Fatalf("WWW-Authenticate : %q", h)
				}
				if strings.Contains(h, "max_age=600") != tc.maxAge {
					t.Fatalf("max_age attendu=%v : %q", tc.maxAge, h)
				}
				if !strings.Contains(rec.Body.String(), "STEP_UP_REQUIRED") {
					t.Fatalf("corps : %s", rec.Body.String())
				}
			}
		})
	}
}

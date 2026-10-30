package iamapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/core/i18n"
	"github.com/thekrauss/kubemanager/internal/iam"
)

// Chaque rôle et chaque permission a son titre (et sa description) en
// anglais : sinon la console et la CLI afficheraient du français à un
// utilisateur anglophone.
func TestRolesAndPermissionsAreTranslated(t *testing.T) {
	for _, r := range iam.PredefinedRoles() {
		for _, text := range []string{r.Title, r.Description} {
			assert.True(t, i18n.Known(text), "rôle %s : %q absent de core/i18n/catalog/roles.json", r.Name, text)
			assert.NotEqual(t, text, i18n.Translate(i18n.EN, text), "rôle %s : pas de version anglaise", r.Name)
		}
	}
	for _, p := range iam.AllPermissions() {
		info, _ := iam.Info(p)
		if info.PlatformOnly {
			continue
		}
		assert.True(t, i18n.Known(info.Title), "permission %s : %q absent du catalogue", p, info.Title)
	}
}

func TestListRoles_FollowsRequestLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Controller{}
	call := func(l i18n.Locale) []RoleView {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/iam/roles", nil).WithContext(i18n.With(context.Background(), l))
		out, err := h.ListRoles(c)
		require.NoError(t, err)
		return out
	}
	byName := func(list []RoleView) map[string]RoleView {
		m := map[string]RoleView{}
		for _, r := range list {
			m[r.Name] = r
		}
		return m
	}
	en, fr := byName(call(i18n.EN)), byName(call(i18n.FR))
	assert.Equal(t, "Viewer", en["roles/viewer"].Title)
	assert.Equal(t, "Lecteur", fr["roles/viewer"].Title)
	assert.Equal(t, "Views resources, logs and metrics.", en["roles/viewer"].Description)
}

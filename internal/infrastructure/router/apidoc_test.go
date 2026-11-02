package router

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/loopfz/gadgeto/tonic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/fizz"

	"github.com/thekrauss/kubemanager/internal/core/i18n"
)

type docInternalOut struct {
	Secret string `json:"secret"`
}

type docPublicOut struct {
	Name string `json:"name"`
}

// Le spec public documente sécurité et erreurs, et ne montre ni les routes
// internes ni leurs schémas.
func TestPublicSpec_HidesInternalRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := fizz.NewFromEngine(gin.New())
	f.Generator().SetSecuritySchemes(securitySchemes())

	root := &RouteGroup{Path: PathAPIRoot}
	pub := root.NewGroup("/things", "").Named("Choses", "Les choses.")
	pub.AddRoute("", http.MethodGet, "Lister les choses (paginé)", Authenticated(),
		tonic.Handler(func(c *gin.Context) (*docPublicOut, error) { return nil, nil }, http.StatusOK))
	pub.AddRoute("/login", http.MethodPost, "Se connecter", Public(),
		tonic.Handler(func(c *gin.Context) (*docPublicOut, error) { return nil, nil }, http.StatusOK))
	adm := root.NewGroup("/admin", "").Named("Admin", "Interne.").Internal()
	adm.AddRoute("", http.MethodGet, "Secret", Authenticated(),
		tonic.Handler(func(c *gin.Context) (*docInternalOut, error) { return nil, nil }, http.StatusOK))
	registerGroup(f.Group(root.Path, "", ""), root, root.Path, false)

	raw, err := publicSpec(f.Generator().API(), i18n.FR)
	require.NoError(t, err)
	var spec struct {
		Paths map[string]map[string]struct {
			Summary   string                    `json:"summary"`
			Tags      []string                  `json:"tags"`
			Security  []map[string][]string     `json:"security"`
			Responses map[string]map[string]any `json:"responses"`
		} `json:"paths"`
		Tags       []struct{ Name string }          `json:"tags"`
		Components struct{ Schemas map[string]any } `json:"components"`
	}
	require.NoError(t, json.Unmarshal(raw, &spec))

	assert.NotContains(t, spec.Paths, PathAPIRoot+"/admin")
	assert.Len(t, spec.Tags, 1)
	assert.Equal(t, "Choses", spec.Tags[0].Name)
	for name := range spec.Components.Schemas {
		assert.NotContains(t, name, "InternalOut", "schéma d'une route interne")
	}

	list := spec.Paths[PathAPIRoot+"/things"]["get"]
	assert.Equal(t, "Lister les choses", list.Summary)
	assert.Equal(t, []string{"Choses"}, list.Tags)
	assert.Len(t, list.Security, 2, "jeton de session ou clé API")
	assert.Contains(t, list.Responses, "403")

	login := spec.Paths[PathAPIRoot+"/things/login"]["post"]
	assert.NotNil(t, login.Security)
	assert.Empty(t, login.Security, "route publique : aucune authentification")
	assert.NotContains(t, login.Responses, "401")
}

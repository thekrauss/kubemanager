package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/fizz"
	"github.com/wI2L/fizz/openapi"

	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	"github.com/thekrauss/kubemanager/internal/core/i18n"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	controller "github.com/thekrauss/kubemanager/internal/modules/auth"
	"github.com/thekrauss/kubemanager/internal/modules/builds"
	clustersCtrl "github.com/thekrauss/kubemanager/internal/modules/clusters"
	"github.com/thekrauss/kubemanager/internal/modules/github"
	"github.com/thekrauss/kubemanager/internal/modules/iamapi"
	identity "github.com/thekrauss/kubemanager/internal/modules/identity"
	"github.com/thekrauss/kubemanager/internal/modules/projects"
	realtimeCtrl "github.com/thekrauss/kubemanager/internal/modules/realtime"
	"github.com/thekrauss/kubemanager/internal/modules/sites"
	workload "github.com/thekrauss/kubemanager/internal/modules/workloads"
)

var (
	docRoutesOnce sync.Once
	docFizz       *fizz.Fizz
)

// documentedAPI enregistre les vraies routes, avec des contrôleurs vides : seul
// le type de leurs méthodes compte pour générer le spec, elles ne sont jamais
// appelées. Une nouvelle route ou un nouveau DTO arrive donc ici tout seul.
func documentedAPI(t *testing.T) *fizz.Fizz {
	t.Helper()
	docRoutesOnce.Do(func() {
		gin.SetMode(gin.TestMode)
		app := &App{
			Security: Security{Middleware: &security.MiddlewareManager{}},
			Repos:    &RepositoryContainer{},
			Controllers: &ControllerContainer{
				APIKey:   struct{ controller.IAPIKeyController }{},
				RBAC:     struct{ controller.IRBACController }{},
				Project:  struct{ projects.IProjectController }{},
				Workload: struct{ workload.IWorkloadController }{},
				Identity: struct{ identity.Controller }{},
				Auth:     &identity.AuthController{},
				Realtime: struct {
					realtimeCtrl.IRealtimeController
				}{},
				Clusters: struct {
					clustersCtrl.IClusterController
				}{},
				Site:   struct{ sites.ISiteController }{},
				IAM:    &iamapi.Controller{},
				Builds: &builds.Controller{},
				GitHub: &github.Controller{},
			},
		}
		docFizz = fizz.NewFromEngine(gin.New())
		configureGenerator(docFizz)
		AddAllRoutes(app)
		require.NoError(t, RegisterRoutes(docFizz))
	})
	require.Empty(t, docFizz.Errors(), "génération du spec")
	return docFizz
}

func decodedSpec(t *testing.T, l i18n.Locale) map[string]any {
	t.Helper()
	raw, err := publicSpec(documentedAPI(t).Generator().API(), l)
	require.NoError(t, err)
	var spec map[string]any
	require.NoError(t, json.Unmarshal(raw, &spec))
	return spec
}

// visitRoutes : routes de tous les groupes, avec leur caractère interne.
func visitRoutes(g *RouteGroup, internal bool, fn func(g *RouteGroup, r *Route, internal bool)) {
	internal = internal || g.internal
	for _, r := range g.Routes {
		fn(g, r, internal)
	}
	for _, child := range g.Groups {
		visitRoutes(child, internal, fn)
	}
}

// docTexts : tout le texte lisible d'un spec, avec son emplacement.
func docTexts(spec map[string]any) map[string]string {
	out := map[string]string{}
	var walk func(v any, at string)
	walk = func(v any, at string) {
		switch t := v.(type) {
		case map[string]any:
			for key, child := range t {
				if s, ok := child.(string); ok && (key == "description" || key == "summary") {
					out[s] = at + "." + key
					continue
				}
				walk(child, at+"/"+key)
			}
		case []any:
			for _, child := range t {
				walk(child, at)
			}
		}
	}
	walk(spec, "")
	if tags, ok := spec["tags"].([]any); ok {
		for _, tag := range tags {
			if name, ok := tag.(map[string]any)["name"].(string); ok {
				out[name] = "tags.name"
			}
		}
	}
	return out
}

// Noms des statuts HTTP (réponses du spec) : mêmes dans les deux langues.
var httpStatusNames = map[string]bool{
	"OK": true, "Created": true, "Accepted": true, "No Content": true, "Bad Request": true,
	"Unauthorized": true, "Forbidden": true, "Not Found": true, "Conflict": true, "Too Many Requests": true,
}

// Pas de traduction à faire : mêmes mots, ou formats et valeurs techniques.
var sameInBothLanguages = map[string]bool{
	"IAM": true, "Builds": true, "PKCE S256": true,
	"authenticated | mfa_required": true, "google | github": true,
	"http://127.0.0.1:<port>/callback": true, "organizations|projects/<uuid>/roles/<id>": true,
}

// TestPublicDoc_EveryTextIsTranslated : tout texte de la documentation publique
// a sa traduction dans core/i18n/catalog/apidoc.json. Une route, une section ou
// une description ajoutée sans traduction casse ce test. Ajouter l'entrée :
//
//	{"fr": "Créer un site", "en": "Create a site"}
func TestPublicDoc_EveryTextIsTranslated(t *testing.T) {
	documentedAPI(t)
	var missing []string
	require1 := func(where, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		en, ok := i18n.Lookup(i18n.EN, text)
		if !ok {
			missing = append(missing, where+"  "+text)
			return
		}
		if en == text && !sameInBothLanguages[text] {
			missing = append(missing, where+"  (non traduit)  "+text)
		}
	}

	// Les routes, y compris celles que le générateur ne sait pas décrire
	// (flux, téléversements) : elles restent au catalogue pour le jour où elles
	// entrent au spec.
	for _, root := range AllGroups {
		visitRoutes(root, false, func(g *RouteGroup, r *Route, internal bool) {
			if internal {
				return
			}
			require1("route "+r.Method+" "+r.Path, r.Description)
			if g.tag.name != "" {
				require1("section", g.tag.name)
				require1("section "+g.tag.name, g.tag.description)
			}
			// Une permission précise passe par le gabarit vérifié plus bas.
			if r.Access.permission == "" {
				require1("accès "+r.Method+" "+r.Path, r.Access.summary)
			}
		})
	}

	// Les textes tirés des DTO, des sections et des schémas de sécurité.
	fr := decodedSpec(t, i18n.FR)
	for text, where := range docTexts(fr) {
		if httpStatusNames[text] {
			continue
		}
		if base, _, isOperation := strings.Cut(text, accessLabel[i18n.FR]); isOperation {
			require1(where, base)
			continue
		}
		if strings.HasSuffix(where, ".summary") {
			continue // tiré de la description de l'opération
		}
		if strings.HasPrefix(where, "/info") {
			continue // apiDescriptionEN
		}
		require1(where, text)
	}
	assert.True(t, i18n.Known(permissionSummaryFormat), "gabarit de la mention d'accès")

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d texte(s) sans traduction anglaise (core/i18n/catalog/apidoc.json) :\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}

// Texte de permission dans access.go, écrit dans le catalogue comme gabarit.
const permissionSummaryFormat = "permission IAM `%s`"

var (
	// Noms d'outils et jargon d'infrastructure, invisibles pour le client de l'API.
	internalJargon = regexp.MustCompile(`(?i)\b(keycloak|helm|kubernetes|k8s|temporal|buildkit|rollout|centrifugo|metrics-server|jwt|jti|pods?|workloads?|provisionner|bootstrap)\b`)
	// Permissions et exemples entre backticks : des identifiants, pas du texte.
	backticked = regexp.MustCompile("`[^`]*`")
	// Accents et mots courants qui trahissent un texte resté en français.
	frenchLeftover = regexp.MustCompile(`(?i)[éèêàçùâîôûœ]|\b(le|la|les|un|une|des|du|de|et|ou|pour|avec|sans|dans|sur|par|est|jeton|clé|compte|projet|domaine|conteneur|créer|lister|supprimer|modifier|défaut|accès)\b`)
)

func TestPublicDoc_NoInternalJargon(t *testing.T) {
	for _, l := range []i18n.Locale{i18n.FR, i18n.EN} {
		for text, where := range docTexts(decodedSpec(t, l)) {
			assert.Empty(t, internalJargon.FindString(identifiersOut(text)), "%s (%s) : %q", where, l, text)
		}
	}
	// Y compris les routes que le générateur ne sait pas encore décrire.
	documentedAPI(t)
	for _, root := range AllGroups {
		visitRoutes(root, false, func(_ *RouteGroup, r *Route, internal bool) {
			if !internal {
				assert.Empty(t, internalJargon.FindString(identifiersOut(r.Description)), "%s %s : %q", r.Method, r.Path, r.Description)
			}
		})
	}
	assert.Empty(t, internalJargon.FindString(identifiersOut(apiDescription)))
	assert.Empty(t, internalJargon.FindString(identifiersOut(apiDescriptionEN)))
}

// identifiersOut retire ce qui n'est pas du texte : blocs et codes, exemples de
// noms de canal, permissions.
func identifiersOut(s string) string {
	s = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(s, "")
	s = backticked.ReplaceAllString(s, "")
	return regexp.MustCompile(`\(ex: [^)]*\)|\(e\.g\. [^)]*\)`).ReplaceAllString(s, "")
}

func TestPublicDoc_EnglishHasNoFrench(t *testing.T) {
	spec := decodedSpec(t, i18n.EN)
	for text, where := range docTexts(spec) {
		assert.Empty(t, frenchLeftover.FindString(identifiersOut(text)), "%s : %q", where, text)
	}
	assert.Empty(t, frenchLeftover.FindString(identifiersOut(apiDescriptionEN)))
}

// Les sections restent cohérentes : chaque opération pointe vers une section
// déclarée, et une section par nom français.
func TestPublicDoc_TagsStayConsistent(t *testing.T) {
	counts := map[i18n.Locale]int{}
	for _, l := range []i18n.Locale{i18n.FR, i18n.EN} {
		spec := decodedSpec(t, l)
		declared := map[string]bool{}
		for _, tag := range spec["tags"].([]any) {
			declared[tag.(map[string]any)["name"].(string)] = true
		}
		counts[l] = len(declared)
		assert.Len(t, spec["tags"], len(declared), "noms de sections uniques")
		for path, item := range spec["paths"].(map[string]any) {
			for method, op := range item.(map[string]any) {
				for _, tag := range op.(map[string]any)["tags"].([]any) {
					assert.True(t, declared[tag.(string)], "%s %s : section %q non déclarée", method, path, tag)
				}
			}
		}
	}
	assert.Equal(t, counts[i18n.FR], counts[i18n.EN])
}

// Seuls les textes changent : les identifiants sont les mêmes dans les deux langues.
func TestPublicDoc_IdentifiersAreNotTranslated(t *testing.T) {
	fr, en := decodedSpec(t, i18n.FR), decodedSpec(t, i18n.EN)
	assert.Equal(t, shapeOf(fr["components"]), shapeOf(en["components"]))
	assert.Equal(t, shapeOf(fr["paths"]), shapeOf(en["paths"]))

	login := en["paths"].(map[string]any)[PathAPIRoot+"/identity/login"].(map[string]any)["post"].(map[string]any)
	assert.Equal(t, "post-identity-login", login["operationId"])
	assert.Contains(t, login["description"], "\n\n**Access**: public.")

	site := en["paths"].(map[string]any)[PathAPIRoot+"/projects/{id}/sites"].(map[string]any)["get"].(map[string]any)
	assert.Contains(t, site["description"], "\n\n**Access**: IAM permission `sites.sites.list`.")
}

// shapeOf : la structure sans les textes traduits ni les noms de sections.
func shapeOf(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range t {
			if _, isText := child.(string); isText && (key == "description" || key == "summary") {
				continue
			}
			if key == "tags" {
				continue
			}
			out[key] = shapeOf(child)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = shapeOf(child)
		}
		return out
	}
	return v
}

// Le texte d'accueil se traduit à la main : mêmes sections, mêmes lignes.
func TestAPIDescription_VersionsStayAligned(t *testing.T) {
	count := func(s, marker string) int {
		n := 0
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, marker) {
				n++
			}
		}
		return n
	}
	for _, marker := range []string{"## ", "- **", "| "} {
		assert.Equal(t, count(apiDescription, marker), count(apiDescriptionEN, marker), "lignes %q", marker)
	}
	for _, id := range []string{"STEP_UP_REQUIRED", "/identity/login", "/identity/refresh", "/identity/session/verify", "X-API-Key", "Idempotency-Key", "Retry-After"} {
		assert.Contains(t, apiDescriptionEN, id)
	}
}

func TestPublicSpecHandler_FollowsRequestLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := fizz.NewFromEngine(gin.New())
	f.Generator().SetInfo(&openapi.Info{Title: apiTitle, Description: apiDescription, Version: apiVersion})
	handler := publicSpecHandler(f)

	get := func(headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
		for k, v := range headers {
			c.Request.Header.Set(k, v)
		}
		handler(c)
		require.Equal(t, http.StatusOK, w.Code)
		var spec map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &spec))
		return w, spec
	}
	description := func(spec map[string]any) string {
		return spec["info"].(map[string]any)["description"].(string)
	}

	w, spec := get(nil)
	assert.Equal(t, apiDescription, description(spec), "français par défaut")
	assert.Equal(t, "public, max-age=300", w.Header().Get("Cache-Control"))
	assert.ElementsMatch(t, []string{httpheader.AcceptLanguage, "Cookie"}, w.Header().Values(httpheader.Vary))

	_, spec = get(map[string]string{httpheader.AcceptLanguage: "en-US,en;q=0.9"})
	assert.Equal(t, apiDescriptionEN, description(spec))
	_, spec = get(map[string]string{httpheader.AcceptLanguage: "fr-FR"})
	assert.Equal(t, apiDescription, description(spec))
	_, spec = get(map[string]string{httpheader.AcceptLanguage: "de"})
	assert.Equal(t, apiDescription, description(spec), "langue non prise en charge")
	_, spec = get(map[string]string{httpheader.AcceptLanguage: "fr", "Cookie": i18n.CookieName + "=en"})
	assert.Equal(t, apiDescriptionEN, description(spec), "le cookie prime, comme sur le reste de l'API")

	// Calculé une fois par langue : un changement ultérieur n'est pas vu.
	f.Generator().API().Info.Title = "changé"
	_, spec = get(map[string]string{httpheader.AcceptLanguage: "en"})
	assert.Equal(t, apiTitle, spec["info"].(map[string]any)["title"])
}

package router

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/wI2L/fizz"
	"github.com/wI2L/fizz/openapi"

	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	"github.com/thekrauss/kubemanager/internal/core/i18n"
)

// Documentation de l'API publique : texte d'accueil, sécurité et erreurs de
// chaque opération, spec filtré (sans les routes internes) servi sans
// authentification à la console (page /docs) et aux générateurs de clients.
//
// Le français est la langue source : les textes sont écrits en français dans
// le code (routes, sections, étiquettes description des DTO) et leur version
// anglaise vient du catalogue (core/i18n/catalog/apidoc.json). Voir
// TestPublicDoc_EveryTextIsTranslated.

const (
	apiTitle   = "betorun API"
	apiVersion = "1.0.0"

	securityBearer = "BearerAuth"
	securityAPIKey = "ApiKeyAuth"
)

const apiDescription = `API de la plateforme betorun : projets, conteneurs, sites statiques,
domaines, comptes de service et IAM. La console et la CLI ` + "`betorun`" + ` l'utilisent ;
tout ce qu'elles font est faisable par un appel direct.

## Authentification

Deux façons de s'authentifier :

- **Jeton de session** : ` + "`Authorization: Bearer <jeton>`" + `. Obtenu à la connexion
  (` + "`POST /identity/login`" + `), valable quelques minutes et renouvelé avec le jeton de
  rafraîchissement (` + "`POST /identity/refresh`" + `).
- **Clé API** d'un compte de service : ` + "`X-API-Key: <clé>`" + `. Pour les scripts et la CI ;
  ses droits sont ceux des rôles accordés au compte de service.

Une action sensible peut exiger une confirmation récente du second facteur : la réponse
est alors ` + "`401`" + ` avec le code ` + "`STEP_UP_REQUIRED`" + `. Confirmez avec
` + "`POST /identity/session/verify`" + `, puis rejouez la requête.

## Erreurs

Toute erreur renvoie un corps JSON stable :

` + "```json" + `
{ "code": "WORKLOAD_NOT_FOUND", "message": "Conteneur introuvable", "error": "Conteneur introuvable" }
` + "```" + `

Testez ` + "`code`" + ` (invariable) ; ` + "`message`" + ` est traduit selon ` + "`Accept-Language`" + ` (fr, en).

| Statut | Signification |
|---|---|
| 400 | Requête invalide (champ manquant, format) |
| 401 | Non authentifié, jeton expiré, ou confirmation d'identité requise |
| 403 | Permission IAM manquante sur la ressource |
| 404 | Ressource introuvable (ou invisible pour l'appelant) |
| 409 | Conflit : nom déjà pris, etag périmé |
| 429 | Trop de requêtes : réessayez après ` + "`Retry-After`" + ` secondes |

## Requêtes rejouables

Les créations acceptent un en-tête ` + "`Idempotency-Key`" + ` (UUID) : la même clé avec le même
corps renvoie la réponse d'origine au lieu d'agir deux fois.`

// apiDescriptionEN : version anglaise du texte d'accueil. Un long markdown se
// relit mieux en entier qu'en clé de catalogue : garder les deux versions
// alignées (mêmes sections, mêmes lignes de tableau).
const apiDescriptionEN = `API of the betorun platform: projects, containers, static sites,
domains, service accounts and IAM. The console and the ` + "`betorun`" + ` CLI are built on it,
so anything they do can also be done with a direct call.

## Authentication

There are two ways to authenticate:

- **Session token**: ` + "`Authorization: Bearer <token>`" + `. Issued at sign-in
  (` + "`POST /identity/login`" + `), valid for a few minutes and renewed with the refresh
  token (` + "`POST /identity/refresh`" + `).
- **API key** of a service account: ` + "`X-API-Key: <key>`" + `. Meant for scripts and CI;
  it carries the roles granted to the service account.

A sensitive action may require a recent second-factor confirmation: the response is then
` + "`401`" + ` with the code ` + "`STEP_UP_REQUIRED`" + `. Confirm with
` + "`POST /identity/session/verify`" + `, then replay the request.

## Errors

Every error returns a stable JSON body:

` + "```json" + `
{ "code": "WORKLOAD_NOT_FOUND", "message": "workload not found", "error": "workload not found" }
` + "```" + `

Match on ` + "`code`" + `, which never changes; ` + "`message`" + ` is translated according to ` + "`Accept-Language`" + ` (fr, en).

| Status | Meaning |
|---|---|
| 400 | Invalid request (missing field, wrong format) |
| 401 | Not authenticated, expired token, or identity confirmation required |
| 403 | Missing IAM permission on the resource |
| 404 | Resource not found (or not visible to the caller) |
| 409 | Conflict: name already taken, stale etag |
| 429 | Too many requests: retry after ` + "`Retry-After`" + ` seconds |

## Replaying requests

Create operations accept an ` + "`Idempotency-Key`" + ` header (UUID): the same key with the same
body returns the original response instead of acting twice.`

// docTag : nom et description d'une section de la documentation.
type docTag struct {
	name, description string
}

// Named : section de la documentation où apparaissent les routes du groupe.
func (g *RouteGroup) Named(name, description string) *RouteGroup {
	g.tag = docTag{name: name, description: description}
	return g
}

// Internal : routes réservées à la plateforme (agents de cluster,
// administration) : absentes du spec public.
func (g *RouteGroup) Internal() *RouteGroup {
	g.internal = true
	return g
}

// docOptions : résumé, accès, sécurité et erreurs d'une opération.
func docOptions(r *Route, internal bool) []fizz.OperationOption {
	summary, description := describe(r)
	opts := []fizz.OperationOption{fizz.Summary(summary), fizz.Description(description)}
	if internal {
		opts = append(opts, fizz.XInternal())
	}
	if r.Access.kind == accessPublic {
		opts = append(opts, fizz.WithoutSecurity())
	} else {
		opts = append(opts,
			fizz.Security(&openapi.SecurityRequirement{securityBearer: []string{}}),
			fizz.Security(&openapi.SecurityRequirement{securityAPIKey: []string{}}))
	}
	for _, status := range errorStatuses(r) {
		if _, ok := r.Responses[status]; ok {
			continue
		}
		opts = append(opts, fizz.Response(strconv.Itoa(status), http.StatusText(status), ErrorBody{}, nil, nil))
	}
	return opts
}

// describe : résumé (avant la première précision entre parenthèses) et
// description complète avec le niveau d'accès.
func describe(r *Route) (summary, description string) {
	base := strings.TrimSpace(r.Description)
	if base == "" {
		base = r.Method + " " + r.Path
	}
	return summaryOf(base), base + accessLabel[i18n.FR] + r.Access.summary + "."
}

// accessLabel : introduit le niveau d'accès à la fin d'une description.
var accessLabel = map[i18n.Locale]string{
	i18n.FR: "\n\n**Accès** : ",
	i18n.EN: "\n\n**Access**: ",
}

func summaryOf(description string) string {
	if i := strings.Index(description, " ("); i > 0 {
		return description[:i]
	}
	return description
}

func errorStatuses(r *Route) []int {
	if r.Access.kind == accessPublic {
		return []int{http.StatusBadRequest, http.StatusTooManyRequests}
	}
	return []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound}
}

// securitySchemes : jeton de session ou clé API.
func securitySchemes() map[string]*openapi.SecuritySchemeOrRef {
	return map[string]*openapi.SecuritySchemeOrRef{
		securityBearer: {SecurityScheme: &openapi.SecurityScheme{
			Type: "http", Scheme: "bearer", BearerFormat: "JWT",
			Description: "Jeton de session obtenu à la connexion.",
		}},
		securityAPIKey: {SecurityScheme: &openapi.SecurityScheme{
			Type: "apiKey", In: "header", Name: "X-API-Key",
			Description: "Clé API d'un compte de service.",
		}},
	}
}

// publicSpecHandler : spec sans les opérations internes ni leurs sections,
// dans la langue de la requête. Calculé à la première demande dans chaque
// langue, quand toutes les routes sont enregistrées.
func publicSpecHandler(f *fizz.Fizz) gin.HandlerFunc {
	type cached struct {
		once sync.Once
		body []byte
		err  error
	}
	specs := map[i18n.Locale]*cached{i18n.FR: {}, i18n.EN: {}}
	return func(c *gin.Context) {
		locale := i18n.FromRequest(c.Request)
		s := specs[locale]
		s.once.Do(func() { s.body, s.err = publicSpec(f.Generator().API(), locale) })
		if s.err != nil {
			c.JSON(http.StatusInternalServerError, ErrorBody{Code: "INTERNAL", Message: s.err.Error(), Error: s.err.Error()})
			return
		}
		c.Header("Cache-Control", "public, max-age=300")
		// La langue vient du cookie btr_locale ou d'Accept-Language
		// (i18n.FromRequest) : un cache doit distinguer les deux.
		addVary(c, httpheader.AcceptLanguage)
		addVary(c, "Cookie")
		c.Data(http.StatusOK, "application/json; charset=utf-8", s.body)
	}
}

// addVary : le middleware de langue pose déjà Accept-Language.
func addVary(c *gin.Context, name string) {
	for _, v := range c.Writer.Header().Values(httpheader.Vary) {
		if strings.EqualFold(v, name) {
			return
		}
	}
	c.Writer.Header().Add(httpheader.Vary, name)
}

func publicSpec(api *openapi.OpenAPI, locale i18n.Locale) ([]byte, error) {
	raw, err := json.Marshal(api)
	if err != nil {
		return nil, err
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	used := map[string]bool{}
	paths, _ := spec["paths"].(map[string]any)
	for p, item := range paths {
		ops, _ := item.(map[string]any)
		for m, op := range ops {
			o, _ := op.(map[string]any)
			if internal, _ := o["x-internal"].(bool); internal {
				delete(ops, m)
				continue
			}
			tags, _ := o["tags"].([]any)
			for _, t := range tags {
				used[t.(string)] = true
			}
		}
		if len(ops) == 0 {
			delete(paths, p)
		}
	}
	tags, _ := spec["tags"].([]any)
	kept := tags[:0]
	for _, t := range tags {
		if name, _ := t.(map[string]any)["name"].(string); used[name] {
			kept = append(kept, t)
		}
	}
	spec["tags"] = kept
	pruneSchemas(spec)
	if locale != i18n.FR {
		localizeSpec(spec, locale)
	}
	return json.Marshal(spec)
}

// localizeSpec traduit les textes du spec, écrits en français. Les identifiants
// (operationId, noms de champs et de schémas, permissions) ne bougent pas.
func localizeSpec(spec map[string]any, locale i18n.Locale) {
	if info, ok := spec["info"].(map[string]any); ok {
		info["description"] = apiDescriptionEN
	}
	tags, _ := spec["tags"].([]any)
	for _, t := range tags {
		if tag, ok := t.(map[string]any); ok {
			tag["name"] = docText(locale, tag["name"])
			tag["description"] = docText(locale, tag["description"])
		}
	}
	paths, _ := spec["paths"].(map[string]any)
	for _, item := range paths {
		ops, _ := item.(map[string]any)
		for _, op := range ops {
			if o, ok := op.(map[string]any); ok {
				localizeOperation(o, locale)
			}
		}
	}
	localizeDescriptions(spec["components"], locale)
}

func localizeOperation(op map[string]any, locale i18n.Locale) {
	tags, _ := op["tags"].([]any)
	for i, t := range tags {
		tags[i] = docText(locale, t)
	}
	text, _ := op["description"].(string)
	base, access, _ := strings.Cut(text, accessLabel[i18n.FR])
	translated, _ := docText(locale, base).(string)
	op["summary"] = summaryOf(translated)
	op["description"] = translated + accessLabel[locale] +
		i18n.Translate(locale, strings.TrimSuffix(access, ".")) + "."
	for _, key := range []string{"parameters", "requestBody", "responses"} {
		localizeDescriptions(op[key], locale)
	}
}

// localizeDescriptions parcourt paramètres et schémas. Un champ qui s'appelle
// lui-même « description » est un objet, pas une chaîne : il n'est pas confondu.
func localizeDescriptions(v any, locale i18n.Locale) {
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			if _, isText := child.(string); isText && key == "description" {
				t[key] = docText(locale, child)
				continue
			}
			localizeDescriptions(child, locale)
		}
	case []any:
		for _, child := range t {
			localizeDescriptions(child, locale)
		}
	}
}

// docText : traduction d'un texte entier ; un texte absent du catalogue reste
// tel quel (les noms de statuts HTTP des réponses, par exemple).
func docText(locale i18n.Locale, v any) any {
	if s, ok := v.(string); ok {
		if out, found := i18n.Lookup(locale, s); found {
			return out
		}
	}
	return v
}

// pruneSchemas : retire les schémas que seules les opérations internes
// utilisaient.
func pruneSchemas(spec map[string]any) {
	components, _ := spec["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if schemas == nil {
		return
	}
	const prefix = "#/components/schemas/"
	reached := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if ref, ok := t["$ref"].(string); ok && strings.HasPrefix(ref, prefix) {
				name := strings.TrimPrefix(ref, prefix)
				if !reached[name] {
					reached[name] = true
					walk(schemas[name])
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(spec["paths"])
	for name := range schemas {
		if !reached[name] {
			delete(schemas, name)
		}
	}
}

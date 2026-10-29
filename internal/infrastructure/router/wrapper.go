package router

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/robertbakker/swaggerui"
	"github.com/sirupsen/logrus"
	"github.com/wI2L/fizz"
	"github.com/wI2L/fizz/openapi"

	"github.com/thekrauss/kubemanager/internal/core/configs"
)

const PathAPIRoot = "/kmanager/v1"

var RootGroup = NewRootGroup(PathAPIRoot, "API principale KUBEMANAGER")

type Route struct {
	Path        string
	Method      string
	Description string
	Handler     gin.HandlerFunc
	ID          string
	Payload     interface{}
	Query       interface{}
	Responses   map[int]interface{}
	// Access : contrôle d'accès, obligatoire (voir access.go).
	Access Access
}

type RouteGroup struct {
	Path        string
	Description string
	Routes      []*Route
	Groups      []*RouteGroup
	Middlewares []gin.HandlerFunc
}

var (
	AllGroups []*RouteGroup
	RouteMap  = make(map[string]*Route)
)

func NewRootGroup(path string, desc string) *RouteGroup {
	g := &RouteGroup{Path: path, Description: desc}
	AllGroups = append(AllGroups, g)
	return g
}

func (g *RouteGroup) NewGroup(path string, desc string) *RouteGroup {
	child := &RouteGroup{Path: path, Description: desc}
	g.Groups = append(g.Groups, child)
	return child
}

func (g *RouteGroup) AddMiddleware(mw ...gin.HandlerFunc) *RouteGroup {
	g.Middlewares = append(g.Middlewares, mw...)
	return g
}

func (g *RouteGroup) AddRoute(path string, method string, desc string, access Access, handler gin.HandlerFunc) *Route {
	r := &Route{
		Method:      method,
		Path:        path,
		Description: desc,
		Handler:     handler,
		Responses:   make(map[int]interface{}),
		Access:      access,
	}
	g.Routes = append(g.Routes, r)
	return r
}

func RegisterSchema(router *fizz.Fizz, title, desc string, l *logrus.Entry) error {
	info := &openapi.Info{
		Title:       title,
		Description: desc,
		Version:     "1.0.0",
	}

	baseSecuritySchemes := map[string]*openapi.SecurityScheme{
		"BearerAuth": {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "Entrez le JWT Bearer Token (Ex: 'Bearer eyJhbGci...')",
		},
	}

	finalSecuritySchemes := make(map[string]*openapi.SecuritySchemeOrRef)
	for name, scheme := range baseSecuritySchemes {
		finalSecuritySchemes[name] = &openapi.SecuritySchemeOrRef{
			SecurityScheme: scheme,
		}
	}

	router.Generator().SetSecuritySchemes(finalSecuritySchemes)

	_ = router.Generator().OverrideDataType(reflect.TypeOf(map[string]any{}), "object", "")

	// Source of truth OpenAPI JSON (public)
	openAPIPath := PathAPIRoot + "/openapi.json"
	openAPIHandler := router.OpenAPI(info, "json")
	router.GET(openAPIPath, nil, openAPIHandler)
	// Backward-compatible alias without API prefix.
	router.GET("/openapi.json", nil, openAPIHandler)

	// Internal Swagger UI (protected)
	swaggerHandler := http.StripPrefix("/swagger/", swaggerui.SwaggerURLHandler(openAPIPath))

	authMiddleware := gin.BasicAuth(gin.Accounts{
		configs.AppConfig.Swagger.User: configs.AppConfig.Swagger.Password,
	})

	router.GET("/swagger/*any", nil, authMiddleware, func(c *gin.Context) {
		swaggerHandler.ServeHTTP(c.Writer, c.Request)
	})

	// Public Scalar UI
	scalarHTML := `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>SCAPI API Docs</title>
</head>
<body>
  <script id="api-reference" data-url="` + openAPIPath + `"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`
	router.GET("/docs", nil, func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(scalarHTML))
	})

	l.Infof("OpenAPI JSON: %s", openAPIPath)
	l.Infof("Swagger UI: /swagger/")
	l.Infof("Scalar UI: /docs")
	return nil
}

func RegisterRoutes(app *fizz.Fizz) error {
	for _, g := range AllGroups {
		registerGroup(app.Group(g.Path, "", g.Description), g, g.Path)
	}
	return nil
}

func registerGroup(fg *fizz.RouterGroup, group *RouteGroup, absPath string) {
	g := fg

	for _, m := range group.Middlewares {
		g.Use(m)
	}

	for _, r := range group.Routes {
		fullPath := buildFullPath(absPath, r.Path)
		opID := r.ID
		if opID == "" {
			opID = generateOperationID(r.Method, fullPath)
		}

		options := []fizz.OperationOption{
			fizz.ID(opID),
			fizz.Description(buildExplicitDescription(r, absPath)),
		}

		if r.Payload != nil {
			options = append(options, fizz.InputModel(r.Payload))
		} else if r.Query != nil {
			options = append(options, fizz.InputModel(r.Query))
		}

		for status, dto := range r.Responses {
			description := http.StatusText(status)
			statusCodeStr := strconv.Itoa(status)
			options = append(options, fizz.Response(
				statusCodeStr,
				description,
				dto,
				nil,
				nil,
			))
		}

		if r.Access.kind == 0 {
			panic("route " + r.Method + " " + fullPath + " has no access control")
		}
		handlers := []gin.HandlerFunc{r.Handler}
		if r.Access.guard != nil {
			handlers = []gin.HandlerFunc{r.Access.guard, r.Handler}
		}
		if r.Access.kind == accessPublic {
			publicRoutes[fullPath] = true
		}
		g.Handle(r.Path, r.Method, options, handlers...)
	}

	for _, child := range group.Groups {
		registerGroup(g.Group(child.Path, "", child.Description), child, absPath+child.Path)
	}
}

func buildExplicitDescription(r *Route, absPath string) string {
	base := strings.TrimSpace(r.Description)
	if base == "" {
		base = "Opération API"
	}
	fullPath := buildFullPath(absPath, r.Path)
	return base + " (Route: " + strings.ToUpper(strings.TrimSpace(r.Method)) + " " + fullPath + ")" +
		"\n\nAccès : " + r.Access.summary + "."
}

// publicRoutes : chemins gin des routes déclarées Public(), remplis à
// l'enregistrement et transmis à l'AuthMiddleware.
var publicRoutes = map[string]bool{}

// PublicRoutes : copie des routes publiques enregistrées.
func PublicRoutes() map[string]bool {
	out := make(map[string]bool, len(publicRoutes))
	for k, v := range publicRoutes {
		out[k] = v
	}
	return out
}

func generateOperationID(method, fullPath string) string {
	// Strip the API root prefix so IDs don't all start with "scapi-v1-"
	trimmed := strings.TrimPrefix(fullPath, PathAPIRoot)
	trimmed = strings.Trim(trimmed, "/")
	// Replace path separators and param markers
	trimmed = strings.ReplaceAll(trimmed, "/", "-")
	trimmed = strings.ReplaceAll(trimmed, ":", "")
	if trimmed == "" {
		trimmed = "root"
	}
	return strings.ToLower(method) + "-" + trimmed
}

func buildFullPath(absPath, relPath string) string {
	full := strings.TrimSuffix(absPath, "/")
	route := strings.TrimSpace(relPath)

	if route == "" {
		if full == "" {
			return "/"
		}
		return full
	}

	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}

	if full == "" {
		return route
	}
	return full + route
}

func (r *Route) AddID(id string) *Route {
	r.ID = id
	RouteMap[r.ID] = r
	return r
}

func (r *Route) AddPayload(dto interface{}) *Route {
	r.Payload = dto
	return r
}

// AddQuery adds the DTO used for the query string params (GET / DELETE).
func (r *Route) AddQuery(dto interface{}) *Route {
	r.Query = dto
	return r
}

// AddResponse adds a response DTO for a given HTTP status.
func (r *Route) AddResponse(status int, description string, dto interface{}) *Route {
	r.Responses[status] = dto
	return r
}

func (r *Route) AddPath(path string) *Route {
	r.Path = path
	return r
}

// wrapRawHandler adapte un handler qui écrit directement sur gin.ResponseWriter
// (pour streaming : logs, SSE, download) en gin.HandlerFunc.
//
// Le handler prend `(*gin.Context, *InputStruct)` — on parse les query/path
// params à la main via tonic pour homogénéité avec les autres routes.
func wrapRawHandler(fn interface{}) gin.HandlerFunc {
	v := reflect.ValueOf(fn)
	t := v.Type()
	if t.Kind() != reflect.Func || t.NumIn() != 2 {
		panic("wrapRawHandler: fn must be func(*gin.Context, *InputStruct)")
	}
	inputType := t.In(1).Elem()

	return func(c *gin.Context) {
		input := reflect.New(inputType).Interface()
		if err := c.ShouldBindUri(input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := c.ShouldBindQuery(input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		v.Call([]reflect.Value{reflect.ValueOf(c), reflect.ValueOf(input)})
	}
}

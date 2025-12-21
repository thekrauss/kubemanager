package router

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/robertbakker/swaggerui"
	"github.com/sirupsen/logrus"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	"github.com/wI2L/fizz"
	"github.com/wI2L/fizz/openapi"
)

const PathAPIRoot = "/scapi/v1"

var RootGroup = NewRootGroup(PathAPIRoot, "API principale SCAPI")

type Route struct {
	Path        string
	Method      string
	Description string
	Handler     gin.HandlerFunc
	ID          string
	Right       string
	Payload     interface{}
	Query       interface{}
	Responses   map[int]interface{}
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

func (g *RouteGroup) AddRoute(path string, method string, desc string, handler gin.HandlerFunc) *Route {
	r := &Route{
		Method:      method,
		Path:        path,
		Description: desc,
		Handler:     handler,
		Responses:   make(map[int]interface{}),
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

	swaggerHandler := http.StripPrefix("/swagger/", swaggerui.SwaggerURLHandler("doc.json"))

	authMiddleware := gin.BasicAuth(gin.Accounts{
		configs.AppConfig.Swagger.User: configs.AppConfig.Swagger.Password,
	})

	router.GET("/swagger/*any", nil, authMiddleware, func(c *gin.Context) {
		if c.Request.URL.Path == "/swagger/doc.json" {
			router.OpenAPI(info, "json")(c)
		} else {
			swaggerHandler.ServeHTTP(c.Writer, c.Request)
		}
	})

	l.Infof("Swagger UI: http://localhost:8080/swagger/#/")
	return nil
}

func RegisterRoutes(app *fizz.Fizz, mw *security.MiddlewareManager) error {
	for _, g := range AllGroups {
		registerGroup(app.Group(g.Path, "", g.Description), g, g.Path, mw)
	}
	return nil
}

func registerGroup(fg *fizz.RouterGroup, group *RouteGroup, absPath string, mw *security.MiddlewareManager) {
	g := fg

	for _, m := range group.Middlewares {
		g.Use(m)
	}

	for _, r := range group.Routes {
		options := []fizz.OperationOption{
			fizz.Description(r.Description),
		}

		if r.ID != "" {
			options = append(options, fizz.ID(r.ID))
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

		handlers := []gin.HandlerFunc{r.Handler}

		if r.Right != "" {
			permissionMiddleware := mw.RequireProjectPermission(r.Right)
			handlers = append([]gin.HandlerFunc{permissionMiddleware}, handlers...)

			options = append(options, fizz.Security(&openapi.SecurityRequirement{
				"BearerAuth": {},
			}))
		}

		g.Handle(r.Path, r.Method, options, handlers...)
	}

	for _, child := range group.Groups {
		registerGroup(g.Group(child.Path, "", child.Description), child, absPath+child.Path, mw)
	}
}

func (r *Route) AddID(id string) *Route {
	r.ID = id
	return r
}

func (r *Route) AddRight(right string) *Route {
	r.Right = right
	RouteMap[r.ID] = r
	return r
}

func (r *Route) AddPayload(dto interface{}) *Route {
	r.Payload = dto
	return r
}

// adds the DTO used for the request body (POST/PUT/PATCH)
func (r *Route) AddQuery(dto interface{}) *Route {
	r.Query = dto
	return r
}

// adds a response DTO for a given HTTP status
func (r *Route) AddResponse(status int, description string, dto interface{}) *Route {
	r.Responses[status] = dto
	return r
}

func (r *Route) AddPath(path string) *Route {
	r.Path = path
	return r
}

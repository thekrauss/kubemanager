package router

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/thekrauss/kubemanager/internal/audit"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/loopfz/gadgeto/tonic"
	"github.com/sirupsen/logrus"
	"github.com/thekrauss/beto-shared/pkg/redis"
	"github.com/thekrauss/kubemanager/internal/middleware"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	"github.com/wI2L/fizz"
	"github.com/wI2L/fizz/openapi"
	ginprometheus "github.com/zsais/go-gin-prometheus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

type TemporalComponents struct {
	Client client.Client
	Worker worker.Worker
}

type Security struct {
	Verifier       *middleware.Verifier
	Middleware     *security.MiddlewareManager
	Audit          *middleware.AuditLogger
	TenantResolver *middleware.TenantResolver
}

type Servers struct {
	HTTP *http.Server
	GRPC *grpc.Server
}

type Observability struct {
	Tracer                trace.Tracer
	TracerShutdown        func()
	TemporalMetricsCloser io.Closer
}

func (a *App) startHTTPServer() {

	engine := gin.New()
	if err := engine.SetTrustedProxies(trustedProxies(a.Config.Server.TrustedProxies)); err != nil {
		a.Logger.Fatalw("invalid server.trusted_proxies", "error", err)
	}

	p := ginprometheus.NewPrometheus("gin")
	p.MetricsPath = "/metrics"
	p.ReqCntURLLabelMappingFn = func(c *gin.Context) string {
		if fp := c.FullPath(); fp != "" {
			return fp
		}
		return "UNKNOWN"
	}
	if addr := a.Config.Metrics.ListenAddr; addr != "" {
		p.SetListenAddress(addr)
	}
	p.Use(engine)

	origins := a.Config.Server.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"http://localhost:3000"}
	}
	engine.Use(cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key", "Idempotency-Key", "X-Request-ID"},
		ExposeHeaders:    []string{"Idempotency-Status", "Retry-After", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	engine.Use(gin.Recovery())
	engine.Use(middleware.SecureHeaders())

	engine.Use(otelgin.Middleware(a.Config.ServiceName))
	engine.Use(func(c *gin.Context) {
		span := trace.SpanFromContext(c.Request.Context())
		scoped := a.Logger
		if span.SpanContext().IsValid() {
			traceID := span.SpanContext().TraceID().String()
			c.Set("trace_id", traceID)
			scoped = scoped.With("trace_id", traceID)
		}
		c.Set(middleware.LoggerContextKey, scoped)
		c.Next()
	})

	// Langue de la requête (cookie btr_locale > Accept-Language > fr), puis
	// traduction des messages des réponses JSON, refus de l'authentification
	// et du rate limit compris (internal/core/i18n).
	engine.Use(middleware.Locale())
	engine.Use(middleware.TranslateMessages())
	engine.Use(middleware.RateLimitMiddleware(middleware.RateLimitConfig{
		Enabled: a.Config.RateLimit.Enabled,
		Limit:   a.Config.RateLimit.Limit,
		Window:  a.Config.RateLimit.Window,
	}, a.Redis))

	engine.Use(a.Security.Middleware.AuthMiddleware())
	// Journal d'audit immuable : écritures et refus (docs/architecture/iam.md §11).
	engine.Use(a.Security.Middleware.AuditMiddleware(&audit.GormWriter{DB: a.DB}))
	engine.Use(middleware.Idempotency())

	engine.GET("/api/v1/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "UP",
			"service": a.Config.ServiceName,
			"role":    "server",
		})
	})

	// Erreurs des handlers tonic : statut selon le type d'erreur (voir errors.go)
	// au lieu du 400 + message brut par défaut.
	tonic.SetErrorHook(newErrorHook(a.Logger))
	// Paramètres de chemin remplis avant la validation du corps (voir bind.go).
	tonic.SetBindHook(bindHook(tonic.DefaultMaxBodyBytes))

	f := fizz.NewFromEngine(engine)

	infos := &openapi.Info{
		Title:       "KUBE MANAGER API",
		Description: "API K8s Manager",
		Version:     "1.0.0",
	}
	_ = RegisterSchema(f, infos.Title, infos.Description, logrus.NewEntry(logrus.StandardLogger()))

	AddAllRoutes(a)

	RegisterRoutes(f)
	// Routes déclarées Public() : seules exemptées d'authentification.
	a.Security.Middleware.SetPublicRoutes(PublicRoutes())

	addr := fmt.Sprintf(":%d", a.Config.Server.HTTPPort)
	a.Servers.HTTP = &http.Server{Addr: addr, Handler: engine}

	go func() {
		a.Logger.Infof("HTTP server listening on %s", addr)
		if err := a.Servers.HTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.Logger.Fatalf("HTTP server error: %v", err)
		}
	}()
}

func (a *App) gracefulShutdownServers(grpcServer *grpc.Server, httpServer *http.Server, shutdownTimeout time.Duration) {
	if grpcServer != nil {
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
			log.Println("gRPC server stopped gracefully")
		case <-time.After(shutdownTimeout):
			log.Println("gRPC server forced stop")
			grpcServer.Stop()
		}
	}

	if httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		} else {
			log.Println("HTTP server stopped gracefully")
		}
	}

	if err := redis.Close(); err != nil {
		log.Printf("redis close error: %v", err)
	}
}

// defaultTrustedProxies : boucle locale (BFF de la console sur la même machine).
var defaultTrustedProxies = []string{"127.0.0.1", "::1"}

func trustedProxies(cfg []string) []string {
	if len(cfg) == 0 {
		return defaultTrustedProxies
	}
	return cfg
}

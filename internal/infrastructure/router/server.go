package router

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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

// Délais du serveur HTTP. Pas de WriteTimeout global : il couperait les flux
// de journaux et les téléchargements longs ; pas de ReadTimeout : il couperait
// les envois d'archives sur connexion lente. ReadHeaderTimeout protège des
// connexions qui n'envoient jamais leurs en-têtes.
const (
	httpReadHeaderTimeout = 10 * time.Second
	httpIdleTimeout       = 120 * time.Second
	// maxMultipartMemory : au-delà, les parties d'un formulaire vont sur disque.
	maxMultipartMemory = 1 << 20
	// defaultMaxBodyBytes : plafond des corps de requête, routes publiques
	// comprises (POST /identity/device/token lit PostForm sans limite propre).
	defaultMaxBodyBytes = 1 << 20
)

// routesWithOwnBodyLimit : routes qui posent leur propre limite (archives,
// webhooks de GitHub jusqu'à 25 Mio). Un plafond global plus bas la masquerait.
var routesWithOwnBodyLimit = []string{
	"/:siteID/deployments",
	"/builds/upload",
	"/github/webhook",
}

// limitRequestBody : plafond des corps par défaut, avant l'authentification.
func limitRequestBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.FullPath()
		for _, suffix := range routesWithOwnBodyLimit {
			if strings.HasSuffix(path, suffix) {
				c.Next()
				return
			}
		}
		if c.Request.ContentLength > limit {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"code":    "REQUEST_TOO_LARGE",
				"error":   "REQUEST_TOO_LARGE",
				"message": "request body too large",
			})
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

func (a *App) startHTTPServer() {
	if a.Config.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.MaxMultipartMemory = maxMultipartMemory
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
	// Adresse dédiée, non publiée : sans elle, /metrics est servi sur le port de
	// l'API, sans authentification (imposée en production, voir configs).
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

	// Santé avant le rate limit et l'authentification : les sondes viennent de
	// la boucle locale et ne doivent ni consommer de quota ni dépendre de Redis.
	engine.GET("/api/v1/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "UP",
			"service": a.Config.ServiceName,
			"role":    "server",
		})
	})
	engine.GET(readinessPath, func(c *gin.Context) {
		status, body := runReadiness(c.Request.Context(), a.readinessChecks(), a.Logger)
		c.JSON(status, body)
	})

	engine.Use(limitRequestBody(defaultMaxBodyBytes))

	// Le rate limit compte par ClientIP() : il suppose SetTrustedProxies plus
	// haut correct, sinon tous les clients partagent l'adresse du proxy.
	engine.Use(middleware.RateLimitMiddleware(middleware.RateLimitConfig{
		Enabled: a.Config.RateLimit.Enabled,
		Limit:   a.Config.RateLimit.Limit,
		Window:  a.Config.RateLimit.Window,
	}, a.Redis))

	engine.Use(a.Security.Middleware.AuthMiddleware())
	// Journal d'audit immuable : écritures et refus (docs/architecture/iam.md §11).
	engine.Use(a.Security.Middleware.AuditMiddleware(&audit.GormWriter{DB: a.DB}))
	engine.Use(middleware.Idempotency())

	// Erreurs des handlers tonic : statut selon le type d'erreur (voir errors.go)
	// au lieu du 400 + message brut par défaut.
	tonic.SetErrorHook(newErrorHook(a.Logger))
	// Paramètres de chemin remplis avant la validation du corps (voir bind.go).
	tonic.SetBindHook(bindHook(tonic.DefaultMaxBodyBytes))

	f := fizz.NewFromEngine(engine)

	_ = RegisterSchema(f, apiTitle, apiDescription, logrus.NewEntry(logrus.StandardLogger()))

	AddAllRoutes(a)

	RegisterRoutes(f)
	// Routes déclarées Public() : seules exemptées d'authentification.
	a.Security.Middleware.SetPublicRoutes(PublicRoutes())

	addr := fmt.Sprintf(":%d", a.Config.Server.HTTPPort)
	a.Servers.HTTP = &http.Server{
		Addr:              addr,
		Handler:           engine,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

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

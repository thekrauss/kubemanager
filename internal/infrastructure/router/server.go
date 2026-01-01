package router

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/thekrauss/beto-shared/pkg/redis"
	"github.com/wI2L/fizz"
	"github.com/wI2L/fizz/openapi"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"google.golang.org/grpc"
)

func (a *App) startHTTPServer() {

	engine := gin.New()

	origins := a.Config.Server.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"http://localhost:3000"}
	}
	engine.Use(cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	engine.Use(gin.Recovery())

	engine.Use(otelgin.Middleware(a.Config.ServiceName))

	engine.Use(a.MiddlewareManager.AuthMiddleware())

	engine.GET("/api/v1/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "UP",
			"service": a.Config.ServiceName,
		})
	})

	f := fizz.NewFromEngine(engine)

	infos := &openapi.Info{
		Title:       "KUBE MANAGER API",
		Description: "API K8s Manager",
		Version:     "1.0.0",
	}
	_ = RegisterSchema(f, infos.Title, infos.Description, logrus.NewEntry(logrus.StandardLogger()))

	AddAllRoutes(a)

	RegisterRoutes(f, a.MiddlewareManager)

	addr := fmt.Sprintf(":%d", a.Config.Server.HTTPPort)
	a.HTTPServer = &http.Server{Addr: addr, Handler: engine}

	go func() {
		a.Logger.Infof("HTTP server listening on %s", addr)
		if err := a.HTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.Logger.Fatalf("HTTP server error: %v", err)
		}
	}()
}

func (a *App) gracefulShutdown(grpcServer *grpc.Server, httpServer *http.Server, shutdownTimeout time.Duration) {

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	<-stop
	log.Println("shutting down servers...")

	if a.TemporalWorker != nil {
		a.TemporalWorker.Stop()
		log.Println("Temporal worker stopped")
	}

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
	} else {
		log.Println("No gRPC server running to stop (skipping)")
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

	// Redis
	if err := redis.Close(); err != nil {
		log.Printf("redis close error: %v", err)
	}
}

package router

import (
	"context"
	"net/http"

	"github.com/thekrauss/beto-shared/pkg/logger"
	"go.opencensus.io/trace"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"gorm.io/gorm"
	"k8s.io/client-go/kubernetes"

	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

type App struct {
	Config *configs.GlobalConfig
	DB     *gorm.DB
	Logger *zap.SugaredLogger

	JWTManager     security.JWTManager
	GRPCServer     *grpc.Server
	HTTPServer     *http.Server
	TemporalClient client.Client
	TemporalWorker worker.Worker
	K8sClient      *kubernetes.Clientset

	MiddlewareManager *security.MiddlewareManager
	Cache             cache.CacheRedis
	TracerShutdown    func()
	Tracer            trace.Tracer

	Repos       *RepositoryContainer
	Services    *ServiceContainer
	Controllers *ControllerContainer
}

func NewApp(cfg *configs.GlobalConfig) *App {
	log := logger.InitLogger(cfg.Logger)
	return &App{Config: cfg, Logger: log}
}

func (a *App) Run(ctx context.Context) error {

	if err := a.initDependencies(); err != nil {
		a.Logger.Fatalw("dependency init failed", "error", err)
		return err
	}
	defer a.TracerShutdown()
	defer a.TemporalClient.Close()

	if err := a.initDomainLayers(); err != nil {
		a.Logger.Fatalw("domain init failed", "error", err)
		return err
	}
	a.TemporalWorker = temporal.StartWorker(a.TemporalClient, a.Config, a.Logger, a.K8sClient, a.DB)
	a.startHTTPServer()

	a.gracefulShutdown(a.GRPCServer, a.HTTPServer, a.Config.Server.ShutdownTimeout)

	a.Logger.Infow("Application stopped gracefully")
	return nil
}

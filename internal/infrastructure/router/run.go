package router

import (
	"context"
	"net/http"

	"go.opencensus.io/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	"github.com/thekrauss/beto-shared/pkg/logger"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/cache"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

type App struct {
	Config            *configs.GlobalConfig
	JWTManager        security.JWTManager
	DB                *gorm.DB
	Logger            *zap.SugaredLogger
	GRPCServer        *grpc.Server
	HTTPServer        *http.Server
	MiddlewareManager *security.MiddlewareManager
	Cache             cache.CacheRedis
	Tracer            trace.Tracer
	TracerShutdown    func()
	Services          *ServiceContainer
	Controllers       *ControllerContainer
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
	// defer a.TemporalClient.Close()
	// defer a.WorkerService.Close()

	if err := a.initDomainLayers(); err != nil {
		a.Logger.Fatalw("domain init failed", "error", err)
		return err
	}

	// go func() {
	// 	if err := a.WorkerService.StartConsuming(context.Background()); err != nil {
	// 		a.Logger.Errorw("Worker Service failed to start consuming", "error", err)
	// 	}
	// }()

	//go a.startTemporalWorker()
	//a.startHTTPServer()
	//a.NotificationWorker.Start()
	//go a.MailWorker.Start()

	gracefulShutdown(a.GRPCServer, a.HTTPServer, a.Config.Server.ShutdownTimeout)

	a.Logger.Infow("Application stopped gracefully")
	return nil
}

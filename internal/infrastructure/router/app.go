package router

import (
	"context"

	"github.com/thekrauss/beto-shared/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	k8sprovider "github.com/thekrauss/kubemanager/internal/infrastructure/kubernetes"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
)

type App struct {
	Config *configs.GlobalConfig
	Logger *zap.SugaredLogger

	DB          *gorm.DB
	K8sProvider *k8sprovider.ProviderK8s
	Cache       cache.CacheRedis
	Temporal    TemporalComponents

	Security      Security
	Servers       Servers
	Observability Observability

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
	defer a.Observability.TracerShutdown()
	defer a.Temporal.Client.Close()
	defer a.Logger.Sync()

	if err := a.initDomainLayers(); err != nil {
		a.Logger.Fatalw("domain init failed", "error", err)
		return err
	}

	workerManager := temporal.NewWorkerManager(temporal.WorkerConfig{
		Client:    a.Temporal.Client,
		Config:    a.Config,
		Logger:    a.Logger,
		K8sClient: a.K8sProvider.Client,
		K8sConfig: a.K8sProvider.Config,
		DB:        a.DB,
		RBACSvc:   a.Services.RBAC,
	})

	a.Temporal.Worker = workerManager.Start()

	a.startHTTPServer()

	a.gracefulShutdown(a.Servers.GRPC, a.Servers.HTTP, a.Config.Server.ShutdownTimeout)

	a.Logger.Infow("Application stopped gracefully")
	return nil
}

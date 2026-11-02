package router

import (
	"context"
	"encoding/json"
	"fmt"
	workloadsRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/iam/sts"
	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	buildActs "github.com/thekrauss/kubemanager/internal/modules/builds/activities"
	githubSvc "github.com/thekrauss/kubemanager/internal/modules/github/service"

	"github.com/redis/go-redis/v9"
	"github.com/thekrauss/beto-shared/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/core/workerpool"
	idempotency "github.com/thekrauss/kubemanager/internal/infrastructure/idempotence"
	k8sprovider "github.com/thekrauss/kubemanager/internal/infrastructure/kubernetes"
	"github.com/thekrauss/kubemanager/internal/infrastructure/realtime"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	identityKC "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
)

type Mode string

const (
	ModeServer Mode = "server"
	ModeWorker Mode = "worker"
	EnvAppMode      = "APP_MODE"
)

func ResolveMode() Mode {
	switch Mode(os.Getenv(EnvAppMode)) {
	case ModeWorker:
		return ModeWorker
	default:
		return ModeServer
	}
}

type App struct {
	Config *configs.GlobalConfig
	Logger *zap.SugaredLogger

	DB                  *gorm.DB
	Redis               *redis.Client
	K8sProvider         *k8sprovider.ProviderK8s
	Cache               cache.CacheRedis
	AuthCache           cache.AuthCache
	Temporal            TemporalComponents
	TemporalDistributor temporal.TaskDistributor
	Keycloak            identityKC.KeycloakClient
	Idempotency         idempotency.AtomicManager
	AsyncPool           *workerpool.Pool          // updates DB async
	UsageAccumulator    *authSvc.UsageAccumulator // batch API key usage
	Publisher           realtime.Publisher
	RealtimeSigner      realtime.TokenSigner
	ObjectStore         objectstore.Store // nil si sites désactivés
	Variables           workloadsRepo.VariableStore
	IAM                 *iam.Engine
	ServiceAccounts     *iam.ServiceAccountService
	ClusterAgents       *iam.ClusterAgents
	STS                 *sts.Service
	OrgPolicies         *iam.OrgPolicyService
	// GitHub : connexion GitHub (Client nil : désactivée).
	GitHub *githubSvc.Service
	// WorkloadDeployer : mise à jour d'un conteneur à la fin d'un build (worker).
	WorkloadDeployer buildActs.Deployer

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

func (a *App) Init(ctx context.Context) error {
	if err := a.initDependencies(); err != nil {
		return fmt.Errorf("dependency init: %w", err)
	}
	if err := a.initDomainLayers(); err != nil {
		return fmt.Errorf("domain init: %w", err)
	}
	return nil
}

func (a *App) RunServer(ctx context.Context) error {
	defer a.shutdownInfra()

	a.startHTTPServer()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	a.Logger.Infow("server shutdown signal received")
	a.gracefulShutdownServers(a.Servers.GRPC, a.Servers.HTTP, a.Config.Server.ShutdownTimeout)
	a.Logger.Infow("server stopped gracefully")
	return nil
}

func (a *App) RunWorker(ctx context.Context) error {
	defer a.shutdownInfra()

	processor := temporal.NewTemporalProcessor(temporal.WorkerConfig{
		Client:       a.Temporal.Client,
		Config:       a.Config,
		Logger:       a.Logger,
		K8sClient:    a.K8sProvider.Client,
		K8sConfig:    a.K8sProvider.Config,
		DB:           a.DB,
		RBACSvc:      a.Services.RBAC,
		Idempotency:  a.Idempotency,
		Publisher:    a.Publisher,
		ObjectStore:  a.ObjectStore,
		Builds:       a.buildActivities(),
		RegistryPull: a.registryPull(),
		Variables:    a.Variables,
	})

	errCh, err := processor.Start()
	if err != nil {
		return fmt.Errorf("temporal processor start: %w", err)
	}
	a.Temporal.Worker = processor.Worker()
	a.Logger.Infow("temporal worker started", "task_queue", a.Config.Temporal.TaskQueue)

	healthSrv := a.startHealthServer()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Une panne du worker doit sortir en erreur : sinon le code 0 ne déclenche
	// aucun redémarrage selon la politique du conteneur, et rien ne la signale.
	var workerErr error
	select {
	case <-ctx.Done():
		a.Logger.Infow("worker shutdown signal received")
	case workerErr = <-errCh:
		if workerErr != nil {
			a.Logger.Errorw("temporal worker failed", "error", workerErr)
		}
	}

	processor.Shutdown()
	if healthSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.Config.Server.ShutdownTimeout)
		defer cancel()
		_ = healthSrv.Shutdown(shutdownCtx)
	}
	if workerErr != nil {
		return fmt.Errorf("temporal worker: %w", workerErr)
	}
	a.Logger.Infow("worker stopped gracefully")
	return nil
}

func (a *App) startHealthServer() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"UP","role":"worker"}`))
	})
	mux.HandleFunc(workerReadyPath, func(w http.ResponseWriter, r *http.Request) {
		status, body := runReadiness(r.Context(), a.readinessChecks(), a.Logger)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
	addr := fmt.Sprintf(":%d", a.Config.Server.HTTPPort)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		a.Logger.Infow("health server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.Logger.Warnw("health server error", "error", err)
		}
	}()
	return srv
}

func (a *App) shutdownInfra() {
	if a.UsageAccumulator != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		a.UsageAccumulator.Stop(flushCtx)
		cancel()
	}
	if a.AsyncPool != nil {
		a.AsyncPool.Shutdown(10 * time.Second)
	}
	if a.Observability.TracerShutdown != nil {
		a.Observability.TracerShutdown()
	}
	if a.Temporal.Client != nil {
		a.Temporal.Client.Close()
	}

	if a.Observability.TemporalMetricsCloser != nil {
		_ = a.Observability.TemporalMetricsCloser.Close()
	}
	if a.Logger != nil {
		_ = a.Logger.Sync()
	}
}

package router

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/thekrauss/beto-shared/pkg/tracing"
	k8sprovider "github.com/thekrauss/kubemanager/internal/infrastructure/kubernetes"
	"go.temporal.io/sdk/client"

	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/infrastructure/database"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	projectRepos "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	wkldomain "github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	workloadsRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
)

func (a *App) initDependencies() error {
	ctx := context.Background()
	a.Logger.Infow("Initializing application dependencies", "service", a.Config.ServiceName)

	if err := a.initTracing(ctx); err != nil {
		return err
	}

	if err := a.initDatabase(); err != nil {
		return err
	}

	if err := a.initCache(ctx); err != nil {
		return err
	}

	if err := a.initTemporalClient(); err != nil {
		return err
	}

	if err := a.initKubernetes(); err != nil {
		return err

	}

	a.initRepositories()
	a.initSecurity()

	if err := a.initDomain(ctx); err != nil {
		return err
	}

	return nil
}

func (a *App) initTracing(ctx context.Context) error {
	if a.Config.Tracing.JaegerEndpoint == "" {
		a.Observability.TracerShutdown = func() {}
		return nil
	}

	_, shutdown, err := tracing.InitTracerProvider(
		ctx,
		a.Config.ServiceName,
		a.Config.Tracing.JaegerEndpoint,
		1.0,
	)
	if err != nil {
		a.Logger.Warnw("Tracing initialization failed", "error", err)
		a.Observability.TracerShutdown = func() {}
		return nil
	}

	a.Observability.TracerShutdown = shutdown
	return nil
}

func (a *App) initDatabase() error {
	a.Logger.Info("initializing Database Provider...")
	provider, err := database.NewDBProvider(a.Config.Database, a.Config.Logger.Level)
	if err != nil {
		return err
	}
	a.DB = provider.DB

	a.Logger.Info("Running Auto-Migration...")
	err = provider.Migrate(
		&authdomain.User{},
		&authdomain.Project{},
		&authdomain.UserSession{},
		&authdomain.ProjectMember{},
		&authdomain.Role{},
		&authdomain.Permission{},
		&authdomain.APIKey{},
		&wkldomain.Workload{},
	)
	if err != nil {
		return fmt.Errorf("auto-migration failed: %w", err)
	}
	return nil
}

func (a *App) initCache(ctx context.Context) error {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", a.Config.Redis.Host, a.Config.Redis.Port),
		Password: a.Config.Redis.Password,
		DB:       a.Config.Redis.DB,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis connection failed: %	w", err)
	}
	a.Cache = cache.NewcacheRedis(rdb, a.Logger)
	return nil
}

func (a *App) initSecurity() {
	a.Security.JWTManager = security.NewJWTManager(&a.Config.JWT, a.Config.ServiceName)
	a.Security.Middleware = security.NewMiddlewareManager(
		a.Config,
		a.Security.JWTManager,
		a.Cache,
		a.Logger,
		a.Repos.Auth,
	)
}

func (a *App) initRepositories() {
	a.Logger.Info("itializing repositories...")
	authRepo := repository.NewAuthRepository(a.DB)
	projectRepo := projectRepos.NewProjectRepository(a.DB)
	workloadRepo := workloadsRepo.NewWorkloadRepository(a.DB)

	a.Repos = &RepositoryContainer{
		Auth:     authRepo,
		Project:  projectRepo,
		Workload: workloadRepo,
	}
}

func (a *App) initDomain(ctx context.Context) error {

	authRepo := repository.NewAuthRepository(a.DB)

	seedCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := authRepo.SeedDefaultRoles(seedCtx); err != nil {
		a.Logger.Errorw("Seeding roles failed", "error", err)
	}

	return nil
}

func (a *App) initTemporalClient() error {
	a.Logger.Info("Connecting to Temporal Server...")

	opts := client.Options{
		HostPort:  a.Config.Temporal.Host,
		Namespace: a.Config.Temporal.Namespace,
	}

	c, err := client.Dial(opts)
	if err != nil {
		return fmt.Errorf("unable to create temporal client: %w", err)
	}

	a.Temporal.Client = c
	a.Logger.Info("Connected to Temporal successfully")
	return nil
}

func (a *App) initKubernetes() error {
	a.Logger.Info("Initializing Kubernetes Provider...")

	k8sProvider, err := k8sprovider.NewKubernetesProvider(a.Config.Kubernetes.KubeConfigPath)
	if err != nil {
		return err
	}

	a.K8sProvider = k8sProvider

	version, err := k8sProvider.GetServerVersion()
	if err != nil {
		a.Logger.Warnw("K8s ping failed", "error", err)
	} else {
		a.Logger.Infow("Connected to Kubernetes", "version", version)
	}

	return nil
}

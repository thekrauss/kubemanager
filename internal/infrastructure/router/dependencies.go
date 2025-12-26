package router

import (
	"context"
	"fmt"
	"time"

	sharedDB "github.com/thekrauss/beto-shared/pkg/db"
	"github.com/thekrauss/beto-shared/pkg/logger"

	"github.com/redis/go-redis/v9"
	"github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/beto-shared/pkg/tracing"
	cache "github.com/thekrauss/kubemanager/internal/infrastructure/cache"
	"github.com/thekrauss/kubemanager/internal/infrastructure/database"
	"github.com/thekrauss/kubemanager/internal/middleware/security"

	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
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

	a.initSecurity()

	if err := a.initDomain(ctx); err != nil {
		return err
	}

	return nil
}

func (a *App) initTracing(ctx context.Context) error {
	if a.Config.Tracing.JaegerEndpoint == "" {
		return nil
	}
	_, shutdown, err := tracing.InitTracerProvider(ctx, a.Config.ServiceName, a.Config.Tracing.JaegerEndpoint, 1.0)
	if err != nil {
		a.Logger.Warnw("Tracing initialization failed", "error", err)
		return nil
	}
	a.TracerShutdown = shutdown
	return nil
}

func (a *App) initDatabase() error {

	cfg := *a.Config
	log := logger.L()

	dbCfg := sharedDB.Config{
		Driver:   cfg.Database.Driver,
		Host:     cfg.Database.Host,
		Port:     cfg.Database.Port,
		User:     cfg.Database.User,
		Password: cfg.Database.Password,
		Name:     cfg.Database.Name,
		SSLMode:  cfg.Database.SSLMode,
		LogLevel: "warn",
	}

	migrationsPath := "./migrations"
	gormDB, err := database.InitDatabase(dbCfg, migrationsPath)
	if err != nil {
		return errors.Wrap(err, errors.CodeDBError, "database initialization failed")
	}
	a.DB = gormDB
	log.Infow("Database connected",
		"driver", cfg.Database.Driver,
		"host", cfg.Database.Host,
		"name", cfg.Database.Name,
	)
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
	a.JWTManager = security.NewJWTManager(&a.Config.JWT, a.Config.ServiceName)
	a.MiddlewareManager = security.NewMiddlewareManager(a.Config, a.JWTManager, a.Cache, a.Logger)
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

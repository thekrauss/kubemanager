package router

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/thekrauss/beto-shared/pkg/tracing"
	cache "github.com/thekrauss/kubemanager/internal/infrastructure/cache"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	dauth "github.com/thekrauss/kubemanager/internal/modules/auth/domain"

	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
	cfg := a.Config.Database
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s TimeZone=UTC",
		cfg.Host, cfg.User, cfg.Password, cfg.Name, cfg.Port, cfg.SSLMode)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, _ := db.DB()
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)

	if err := db.AutoMigrate(
		&dauth.Permission{}, &dauth.Role{}, &dauth.User{},
		&dauth.Project{}, &dauth.UserSession{}, &dauth.APIKey{},
		&dauth.ProjectMember{},
	); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	a.DB = db
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

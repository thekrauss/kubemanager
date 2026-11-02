package database

import (
	"time"

	sharedDB "github.com/thekrauss/beto-shared/pkg/db"
	"github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/uptrace/opentelemetry-go-extra/otelgorm"
	"gorm.io/gorm"
)

type DBProvider struct {
	DB *gorm.DB
}

// poolSettings : réglages du pool, défauts appliqués aux valeurs absentes.
func poolSettings(cfg configs.DBConfig) (maxOpen, maxIdle int, lifetime, idleTime time.Duration) {
	maxOpen, maxIdle, lifetime, idleTime = cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime
	if maxOpen <= 0 {
		maxOpen = DefaultMaxOpenConns
	}
	if maxIdle <= 0 {
		maxIdle = min(DefaultMaxIdleConns, maxOpen)
	}
	if lifetime <= 0 {
		lifetime = DefaultConnMaxLifetime
	}
	if idleTime <= 0 {
		idleTime = DefaultConnMaxIdleTime
	}
	return maxOpen, maxIdle, lifetime, idleTime
}

func NewDBProvider(cfg configs.DBConfig, logLevel string) (*DBProvider, error) {
	dbCfg := sharedDB.Config{
		Driver:   cfg.Driver,
		Host:     cfg.Host,
		Port:     cfg.Port,
		User:     cfg.User,
		Password: cfg.Password,
		Name:     cfg.Name,
		SSLMode:  cfg.SSLMode,
		LogLevel: logLevel,
	}
	dbCfg.MaxOpenConns, dbCfg.MaxIdleConns, dbCfg.ConnMaxLifetime, dbCfg.ConnMaxIdleTime = poolSettings(cfg)

	migrationsPath := cfg.MigrationsPath
	if migrationsPath == "" {
		migrationsPath = DefaultMigrationsPath
	}
	if cfg.SkipMigrations {
		migrationsPath = ""
	}
	gormDB, err := InitDatabase(dbCfg, migrationsPath)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeDBError, "database initialization failed")
	}

	// OTEL GORM plugin : crée un span par query + expose les metrics standard
	// (histogram query duration, counter errors). Prend le Tracer du global
	// TracerProvider installé par initTracing().
	if err := gormDB.Use(otelgorm.NewPlugin(
		otelgorm.WithDBName(cfg.Name),
		otelgorm.WithAttributes(),
	)); err != nil {
		return nil, errors.Wrap(err, errors.CodeDBError, "failed to install otelgorm plugin")
	}

	return &DBProvider{DB: gormDB}, nil
}

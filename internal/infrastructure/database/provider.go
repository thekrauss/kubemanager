package database

import (
	sharedDB "github.com/thekrauss/beto-shared/pkg/db"
	"github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/uptrace/opentelemetry-go-extra/otelgorm"
	"gorm.io/gorm"
)

type DBProvider struct {
	DB *gorm.DB
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

	migrationsPath := cfg.MigrationsPath
	if migrationsPath == "" {
		migrationsPath = DefaultMigrationsPath
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

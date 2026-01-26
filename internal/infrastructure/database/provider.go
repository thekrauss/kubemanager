package database

import (
	sharedDB "github.com/thekrauss/beto-shared/pkg/db"
	"github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/core/configs"
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

	migrationsPath := "./migrations"
	gormDB, err := InitDatabase(dbCfg, migrationsPath)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeDBError, "database initialization failed")
	}

	return &DBProvider{DB: gormDB}, nil
}

func (p *DBProvider) Migrate(models ...interface{}) error {
	return p.DB.AutoMigrate(models...)
}

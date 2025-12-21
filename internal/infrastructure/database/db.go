package database

import (
	sharedDB "github.com/thekrauss/beto-shared/pkg/db"
	"gorm.io/gorm"

	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func InitDatabase(cfg sharedDB.Config, migrationsPath string) (*gorm.DB, error) {
	gormDB, err := sharedDB.OpenDatabase(cfg)
	if err != nil {
		return nil, err
	}

	_, err = gormDB.DB()
	if err != nil {
		return nil, err
	}

	if err := sharedDB.RunMigrationsWithURL(cfg.ToURL(), migrationsPath); err != nil {
		return nil, err
	}
	return gormDB, nil
}

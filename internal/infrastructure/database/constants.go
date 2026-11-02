package database

import "time"

// DefaultMigrationsPath : répertoire des migrations si la configuration n'en
// donne pas (relatif au répertoire de travail).
const DefaultMigrationsPath = "./migrations"

// PoolDefaults : pool d'un processus sur une petite machine. API et worker
// partagent le même Postgres avec Temporal, Keycloak et static-edge :
// docker-compose.prod.yaml détaille le calcul de max_connections.
const (
	DefaultMaxOpenConns    = 15
	DefaultMaxIdleConns    = 5
	DefaultConnMaxLifetime = 30 * time.Minute
	DefaultConnMaxIdleTime = 5 * time.Minute
)

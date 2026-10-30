package main

import "time"

// Variables d'environnement de configuration de l'edge.
const (
	envHTTPAddr     = "EDGE_HTTP_ADDR"
	envInternalAddr = "EDGE_INTERNAL_ADDR"
	envDatabaseURL  = "EDGE_DATABASE_URL"
	envBaseDomain   = "EDGE_BASE_DOMAIN"
	envCacheTTL     = "EDGE_CACHE_TTL"
	envS3Endpoint   = "EDGE_S3_ENDPOINT"
	envS3AccessKey  = "EDGE_S3_ACCESS_KEY"
	envS3SecretKey  = "EDGE_S3_SECRET_KEY"
	envS3Bucket     = "EDGE_S3_BUCKET"
	envS3Region     = "EDGE_S3_REGION"
	envS3UseSSL     = "EDGE_S3_USE_SSL"
)

// Valeurs par défaut (candidates à la configuration).
const (
	defaultHTTPAddr     = ":8090"
	defaultInternalAddr = ":8091"
	defaultCacheTTL     = 10 * time.Second
)

// Base de données.
const (
	sqlDriverName     = "pgx"
	dbMaxOpenConns    = 10
	dbConnMaxIdleTime = 5 * time.Minute
)

// Serveurs HTTP.
const (
	publicReadHeaderTimeout   = 10 * time.Second
	publicIdleTimeout         = 120 * time.Second
	internalReadHeaderTimeout = 5 * time.Second
	shutdownTimeout           = 15 * time.Second
)

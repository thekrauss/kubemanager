// static-edge : sert les sites statiques depuis l'object store.
//
// Binaire séparé de la plateforme (cmd/gateway) : il est exposé à Internet et
// ne doit embarquer ni identifiants admin, ni accès en écriture. Il lit :
//   - Postgres en lecture seule (nom d'hôte → site + déploiement actif) ;
//   - l'object store en lecture seule (fichiers des déploiements).
//
// Caddy est devant : TLS (certificats à la demande validés par /check sur le
// port interne) puis reverse_proxy vers le port public.
//
// Configuration (variables d'environnement) :
//
//	EDGE_HTTP_ADDR       port public            (défaut :8090)
//	EDGE_INTERNAL_ADDR   /check + /healthz      (défaut :8091)
//	EDGE_DATABASE_URL    DSN Postgres, utilisateur lecture seule (requis)
//	EDGE_BASE_DOMAIN     domaine des sous-domaines de sites (requis)
//	EDGE_CACHE_TTL       cache nom d'hôte → site (défaut 10s)
//	EDGE_S3_ENDPOINT, EDGE_S3_ACCESS_KEY, EDGE_S3_SECRET_KEY, EDGE_S3_BUCKET (requis)
//	EDGE_S3_REGION, EDGE_S3_USE_SSL
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/thekrauss/kubemanager/internal/edge"
	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("static-edge stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	db, err := sql.Open("pgx", cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	db.SetConnMaxIdleTime(5 * time.Minute)

	store, err := objectstore.New(cfg.store)
	if err != nil {
		return err
	}

	srv := &edge.Server{
		Resolver: edge.NewCachedResolver(&edge.SQLResolver{DB: db, BaseDomain: cfg.baseDomain}, cfg.cacheTTL, 0),
		Store:    store,
		Logger:   logger,
	}
	public := &http.Server{
		Addr:              cfg.httpAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	internal := &http.Server{
		Addr:              cfg.internalAddr,
		Handler:           srv.InternalHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	for _, s := range []*http.Server{public, internal} {
		go func(s *http.Server) {
			logger.Info("listening", "addr", s.Addr)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}(s)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = internal.Shutdown(shutdownCtx)
	return public.Shutdown(shutdownCtx)
}

type config struct {
	httpAddr     string
	internalAddr string
	databaseURL  string
	baseDomain   string
	cacheTTL     time.Duration
	store        objectstore.Config
}

func loadConfig() (config, error) {
	cfg := config{
		httpAddr:     envOr("EDGE_HTTP_ADDR", ":8090"),
		internalAddr: envOr("EDGE_INTERNAL_ADDR", ":8091"),
		databaseURL:  os.Getenv("EDGE_DATABASE_URL"),
		baseDomain:   strings.TrimSuffix(strings.ToLower(os.Getenv("EDGE_BASE_DOMAIN")), "."),
		cacheTTL:     10 * time.Second,
		store: objectstore.Config{
			Endpoint:  os.Getenv("EDGE_S3_ENDPOINT"),
			AccessKey: os.Getenv("EDGE_S3_ACCESS_KEY"),
			SecretKey: os.Getenv("EDGE_S3_SECRET_KEY"),
			Bucket:    os.Getenv("EDGE_S3_BUCKET"),
			Region:    os.Getenv("EDGE_S3_REGION"),
		},
	}
	if v := os.Getenv("EDGE_CACHE_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("EDGE_CACHE_TTL: %w", err)
		}
		cfg.cacheTTL = d
	}
	if v := os.Getenv("EDGE_S3_USE_SSL"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return cfg, fmt.Errorf("EDGE_S3_USE_SSL: %w", err)
		}
		cfg.store.UseSSL = b
	}

	var missing []string
	for name, v := range map[string]string{
		"EDGE_DATABASE_URL":  cfg.databaseURL,
		"EDGE_BASE_DOMAIN":   cfg.baseDomain,
		"EDGE_S3_ENDPOINT":   cfg.store.Endpoint,
		"EDGE_S3_ACCESS_KEY": cfg.store.AccessKey,
		"EDGE_S3_SECRET_KEY": cfg.store.SecretKey,
		"EDGE_S3_BUCKET":     cfg.store.Bucket,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return cfg, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

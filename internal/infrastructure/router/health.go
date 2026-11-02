package router

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.temporal.io/sdk/client"
	"go.uber.org/zap"
)

const (
	// readinessPath : prêt à servir (base, Redis, Temporal), pour les
	// healthchecks. Bloqué sur l'hôte public par Caddy.
	readinessPath = "/api/v1/ready"
	// workerReadyPath : équivalent sur le serveur de santé du worker.
	workerReadyPath = "/readyz"
	// readinessTimeout : budget de chaque contrôle, sous le timeout du healthcheck.
	readinessTimeout = 3 * time.Second

	readinessStatusReady    = "READY"
	readinessStatusNotReady = "NOT_READY"
	readinessCheckOK        = "ok"
	readinessCheckDown      = "down"
)

// readinessCheck : un contrôle de dépendance.
type readinessCheck struct {
	name string
	run  func(ctx context.Context) error
}

// readinessChecks : base, Redis et Temporal, communs à l'API et au worker.
func (a *App) readinessChecks() []readinessCheck {
	return []readinessCheck{
		{name: "database", run: func(ctx context.Context) error {
			if a.DB == nil {
				return errors.New("database not initialized")
			}
			sqlDB, err := a.DB.DB()
			if err != nil {
				return err
			}
			return sqlDB.PingContext(ctx)
		}},
		{name: "redis", run: func(ctx context.Context) error {
			if a.Redis == nil {
				return errors.New("redis not initialized")
			}
			return a.Redis.Ping(ctx).Err()
		}},
		{name: "temporal", run: func(ctx context.Context) error {
			if a.Temporal.Client == nil {
				return errors.New("temporal not initialized")
			}
			_, err := a.Temporal.Client.CheckHealth(ctx, &client.CheckHealthRequest{})
			return err
		}},
	}
}

// runReadiness lance les contrôles en parallèle. La réponse n'indique que
// ok/down : les détails (adresses, messages des pilotes) vont au journal.
func runReadiness(ctx context.Context, checks []readinessCheck, logger *zap.SugaredLogger) (int, gin.H) {
	ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()

	results := make(map[string]string, len(checks))
	status := http.StatusOK
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, check := range checks {
		wg.Add(1)
		go func(check readinessCheck) {
			defer wg.Done()
			err := check.run(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[check.name] = readinessCheckDown
				status = http.StatusServiceUnavailable
				if logger != nil {
					logger.Warnw("readiness check failed", "check", check.name, "error", err)
				}
				return
			}
			results[check.name] = readinessCheckOK
		}(check)
	}
	wg.Wait()

	overall := readinessStatusReady
	if status != http.StatusOK {
		overall = readinessStatusNotReady
	}
	return status, gin.H{"status": overall, "checks": results}
}

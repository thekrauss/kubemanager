// Package edge : cœur du binaire static-edge, qui sert les sites statiques.
// Volontairement minimal en dépendances (database/sql + object store) : il
// n'importe ni la config globale, ni Temporal, ni Kubernetes, ni Keycloak.
package edge

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

// Site : ce que l'edge doit savoir pour servir un nom d'hôte.
type Site struct {
	ID           string
	DeploymentID string
	SPAFallback  bool
}

// Prefix : même format que domain.ObjectPrefix côté API (internal/modules/sites).
func (s *Site) Prefix() string {
	return "sites/" + s.ID + "/" + s.DeploymentID + "/"
}

// Resolver : nom d'hôte → site servi, ou nil si inconnu (ou non servable).
type Resolver interface {
	Resolve(ctx context.Context, host string) (*Site, error)
}

// SQLResolver interroge Postgres en lecture seule. Ne sert que les sites LIVE
// avec un déploiement actif, et les domaines personnalisés vérifiés.
type SQLResolver struct {
	DB         *sql.DB
	BaseDomain string
}

const (
	querySubdomain = `SELECT id, active_deployment_id, spa_fallback FROM sites
		WHERE subdomain = $1 AND status = 'LIVE' AND active_deployment_id IS NOT NULL`
	queryCustomDomain = `SELECT s.id, s.active_deployment_id, s.spa_fallback
		FROM site_domains d JOIN sites s ON s.id = d.site_id
		WHERE d.hostname = $1 AND d.verified AND s.status = 'LIVE' AND s.active_deployment_id IS NOT NULL`
)

func (r *SQLResolver) Resolve(ctx context.Context, host string) (*Site, error) {
	query, arg := queryCustomDomain, host
	if sub, ok := strings.CutSuffix(host, "."+r.BaseDomain); ok {
		if sub == "" || strings.Contains(sub, ".") {
			return nil, nil // un seul niveau de sous-domaine
		}
		query, arg = querySubdomain, sub
	}
	var s Site
	err := r.DB.QueryRowContext(ctx, query, arg).Scan(&s.ID, &s.DeploymentID, &s.SPAFallback)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CachedResolver : cache mémoire à TTL court, y compris des réponses
// négatives (protège la DB des scans de noms d'hôte). Un nouveau déploiement
// ou un domaine vérifié est donc visible au plus TTL plus tard.
type CachedResolver struct {
	Next       Resolver
	TTL        time.Duration
	MaxEntries int

	mu      sync.Mutex
	entries map[string]cacheEntry
	now     func() time.Time
}

type cacheEntry struct {
	site    *Site
	expires time.Time
}

func NewCachedResolver(next Resolver, ttl time.Duration, maxEntries int) *CachedResolver {
	if maxEntries <= 0 {
		maxEntries = 100000
	}
	return &CachedResolver{Next: next, TTL: ttl, MaxEntries: maxEntries, entries: map[string]cacheEntry{}, now: time.Now}
}

func (c *CachedResolver) Resolve(ctx context.Context, host string) (*Site, error) {
	now := c.now()
	c.mu.Lock()
	e, ok := c.entries[host]
	c.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.site, nil
	}

	site, err := c.Next.Resolve(ctx, host)
	if err != nil {
		return nil, err // erreurs jamais mises en cache
	}
	c.mu.Lock()
	if len(c.entries) >= c.MaxEntries {
		c.entries = map[string]cacheEntry{} // borne mémoire : purge simple
	}
	c.entries[host] = cacheEntry{site: site, expires: now.Add(c.TTL)}
	c.mu.Unlock()
	return site, nil
}

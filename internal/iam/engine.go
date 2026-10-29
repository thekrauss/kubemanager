package iam

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Binding : un rôle accordé à un principal sur une ressource.
type Binding struct {
	Resource  ResourceName
	Role      string
	Principal Principal
	Condition *Condition
}

// ErrResourceNotFound : la ressource (ou un de ses parents) n'existe pas.
var ErrResourceNotFound = errors.New("resource not found")

// Store : lecture des données IAM. Implémenté en Postgres (GormStore).
type Store interface {
	// BindingsFor : tous les bindings dont le principal figure dans principals.
	BindingsFor(ctx context.Context, principals []Principal) ([]Binding, error)
	// ProjectOrganization : organisation parente d'un projet.
	ProjectOrganization(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error)
}

// Decision : résultat d'un Check, avec sa justification (audit, débogage).
type Decision struct {
	Allowed bool
	// Role et Resource : binding qui a accordé l'accès.
	Role     string
	Resource ResourceName
	Reason   string
}

// Engine : point de décision unique.
//
// Cache : bindings par principal et organisation par projet, TTL court.
//   - un droit accordé est effectif immédiatement (règle de fraîcheur des refus) ;
//   - un droit retiré est diffusé (OnBindingsChanged → Invalidate, y compris
//     entre process via Redis) et, au pire, expire avec le cache (CacheTTL).
type Engine struct {
	store    Store
	cacheTTL time.Duration
	now      func() time.Time

	mu       sync.Mutex
	bindings map[Principal]cachedBindings
	orgs     map[uuid.UUID]cachedOrg
}

type cachedBindings struct {
	byResource map[ResourceName][]Binding
	expires    time.Time
}

type cachedOrg struct {
	org     uuid.UUID
	expires time.Time
}

// DefaultCacheTTL : délai maximal de propagation d'un retrait de droit entre réplicas.
const DefaultCacheTTL = 5 * time.Second

// Règle de fraîcheur : un refus n'est jamais prononcé sur la base du cache.
// S'il venait du cache, les bindings sont relus avant de répondre. Un droit
// qui vient d'être accordé (projet créé par le worker, invitation acceptée)
// est donc effectif immédiatement, sur toutes les réplicas. Le coût (une
// requête indexée) ne porte que sur les refus, bornés par le rate limit.

func NewEngine(store Store, cacheTTL time.Duration) *Engine {
	if cacheTTL < 0 {
		cacheTTL = 0
	}
	return &Engine{
		store:    store,
		cacheTTL: cacheTTL,
		now:      time.Now,
		bindings: map[Principal]cachedBindings{},
		orgs:     map[uuid.UUID]cachedOrg{},
	}
}

// Ancestry : la ressource puis tous ses parents, jusqu'à "platform".
func (e *Engine) Ancestry(ctx context.Context, r ResourceName) ([]ResourceName, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	out := r.localAncestry()
	if r == Platform {
		return out, nil
	}
	if projectID, ok := r.Project(); ok {
		orgID, err := e.projectOrganization(ctx, projectID)
		if err != nil {
			return nil, err
		}
		out = append(out, OrganizationName(orgID))
	}
	return append(out, Platform), nil
}

// Check : p a-t-il perm sur r (directement ou par héritage) ?
func (e *Engine) Check(ctx context.Context, p Principal, perm Permission, r ResourceName) (Decision, error) {
	decisions, err := e.check(ctx, p, []Permission{perm}, r)
	if err != nil {
		return Decision{}, err
	}
	return decisions[0], nil
}

// TestPermissions : sous-ensemble de perms accordé à p sur r. Sert à la
// console pour afficher ou masquer les actions.
func (e *Engine) TestPermissions(ctx context.Context, p Principal, r ResourceName, perms []Permission) ([]Permission, error) {
	decisions, err := e.check(ctx, p, perms, r)
	if err != nil {
		return nil, err
	}
	granted := make([]Permission, 0, len(perms))
	for i, d := range decisions {
		if d.Allowed {
			granted = append(granted, perms[i])
		}
	}
	return granted, nil
}

func (e *Engine) check(ctx context.Context, p Principal, perms []Permission, r ResourceName) ([]Decision, error) {
	for _, perm := range perms {
		if !Known(perm) {
			return nil, fmt.Errorf("iam: unknown permission %q", perm)
		}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	ancestry, err := e.Ancestry(ctx, r)
	if err != nil {
		return nil, err
	}
	byResource, fromCache, err := e.principalBindings(ctx, p, false)
	if err != nil {
		return nil, err
	}

	now := e.now()
	out := make([]Decision, len(perms))
	denied := false
	for i, perm := range perms {
		out[i] = decide(byResource, ancestry, perm, now)
		denied = denied || !out[i].Allowed
	}
	if denied && fromCache {
		if byResource, _, err = e.principalBindings(ctx, p, true); err != nil {
			return nil, err
		}
		for i, perm := range perms {
			if !out[i].Allowed {
				out[i] = decide(byResource, ancestry, perm, now)
			}
		}
	}
	return out, nil
}

func decide(byResource map[ResourceName][]Binding, ancestry []ResourceName, perm Permission, now time.Time) Decision {
	info, _ := Info(perm)
	for _, res := range ancestry {
		if info.PlatformOnly && res != Platform {
			continue
		}
		for _, b := range byResource[res] {
			role, ok := LookupRole(b.Role)
			if !ok || !role.has(perm) {
				continue
			}
			if role.PlatformOnly && res != Platform {
				continue // rôle staff accordé hors platform : ignoré
			}
			if !conditionHolds(b.Condition, now) {
				continue
			}
			return Decision{Allowed: true, Role: b.Role, Resource: res,
				Reason: fmt.Sprintf("%s via %s on %s", perm, b.Role, res)}
		}
	}
	return Decision{Reason: fmt.Sprintf("no binding grants %s", perm)}
}

// expand : le principal et les principals spéciaux qui l'englobent. Les
// groupes s'ajouteront ici (phase B).
func expand(p Principal) []Principal {
	out := []Principal{p, AllUsers}
	if kind, _, err := p.Parse(); err == nil && (kind == KindUser || kind == KindServiceAccount) {
		out = append(out, AllAuthenticatedUsers)
	}
	return out
}

// principalBindings : bindings de p indexés par ressource ; fromCache indique
// s'ils viennent du cache.
func (e *Engine) principalBindings(ctx context.Context, p Principal, force bool) (map[ResourceName][]Binding, bool, error) {
	now := e.now()
	if !force {
		e.mu.Lock()
		c, ok := e.bindings[p]
		e.mu.Unlock()
		if ok && now.Before(c.expires) {
			return c.byResource, true, nil
		}
	}

	list, err := e.store.BindingsFor(ctx, expand(p))
	if err != nil {
		return nil, false, fmt.Errorf("iam: load bindings: %w", err)
	}
	byResource := make(map[ResourceName][]Binding, len(list))
	for _, b := range list {
		byResource[b.Resource] = append(byResource[b.Resource], b)
	}
	if e.cacheTTL > 0 {
		e.mu.Lock()
		e.bindings[p] = cachedBindings{byResource: byResource, expires: now.Add(e.cacheTTL)}
		e.mu.Unlock()
	}
	return byResource, false, nil
}

func (e *Engine) projectOrganization(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	now := e.now()
	e.mu.Lock()
	c, ok := e.orgs[projectID]
	e.mu.Unlock()
	if ok && now.Before(c.expires) {
		return c.org, nil
	}
	org, err := e.store.ProjectOrganization(ctx, projectID)
	if err != nil {
		return uuid.Nil, err
	}
	// Un projet ne change (presque) jamais d'organisation : cache plus long.
	if e.cacheTTL > 0 {
		e.mu.Lock()
		e.orgs[projectID] = cachedOrg{org: org, expires: now.Add(60 * e.cacheTTL)}
		e.mu.Unlock()
	}
	return org, nil
}

// Invalidate : à appeler après toute écriture touchant les bindings de p.
func (e *Engine) Invalidate(p Principal) {
	e.mu.Lock()
	delete(e.bindings, p)
	if p == AllUsers || p == AllAuthenticatedUsers {
		e.bindings = map[Principal]cachedBindings{}
	}
	e.mu.Unlock()
}

// InvalidateProject : à appeler quand un projet change d'organisation.
func (e *Engine) InvalidateProject(projectID uuid.UUID) {
	e.mu.Lock()
	delete(e.orgs, projectID)
	e.mu.Unlock()
}

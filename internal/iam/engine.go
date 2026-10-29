package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// GroupResolver : groupes dont un principal est membre (optionnel pour un Store).
type GroupResolver interface {
	GroupsOf(ctx context.Context, p Principal) ([]Principal, error)
}

// CustomRoleResolver : rôles personnalisés existants parmi names (optionnel).
type CustomRoleResolver interface {
	CustomRoles(ctx context.Context, names []string) (map[string]Role, error)
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
	// custom : rôles personnalisés référencés par ces bindings.
	custom  map[string]Role
	expires time.Time
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
	cached, fromCache, err := e.principalBindings(ctx, p, false)
	if err != nil {
		return nil, err
	}

	now := e.now()
	out := make([]Decision, len(perms))
	denied := false
	for i, perm := range perms {
		out[i] = decide(cached, ancestry, perm, now)
		denied = denied || !out[i].Allowed
	}
	if denied && fromCache {
		if cached, _, err = e.principalBindings(ctx, p, true); err != nil {
			return nil, err
		}
		for i, perm := range perms {
			if !out[i].Allowed {
				out[i] = decide(cached, ancestry, perm, now)
			}
		}
	}
	return out, nil
}

func decide(c cachedBindings, ancestry []ResourceName, perm Permission, now time.Time) Decision {
	byResource := c.byResource
	info, _ := Info(perm)
	for _, res := range ancestry {
		if info.PlatformOnly && res != Platform {
			continue
		}
		for _, b := range byResource[res] {
			role, ok := LookupRole(b.Role)
			if !ok {
				role, ok = c.custom[b.Role] // rôle personnalisé supprimé : absent, n'accorde rien
			}
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

// expand : le principal, ses groupes et les principals spéciaux qui l'englobent.
func (e *Engine) expand(ctx context.Context, p Principal) ([]Principal, error) {
	out := []Principal{p, AllUsers}
	kind, _, err := p.Parse()
	if err != nil || (kind != KindUser && kind != KindServiceAccount) {
		return out, nil
	}
	out = append(out, AllAuthenticatedUsers)
	if gr, ok := e.store.(GroupResolver); ok {
		groups, err := gr.GroupsOf(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, groups...)
	}
	return out, nil
}

// principalBindings : bindings de p indexés par ressource ; fromCache indique
// s'ils viennent du cache.
func (e *Engine) principalBindings(ctx context.Context, p Principal, force bool) (cachedBindings, bool, error) {
	now := e.now()
	if !force {
		e.mu.Lock()
		c, ok := e.bindings[p]
		e.mu.Unlock()
		if ok && now.Before(c.expires) {
			return c, true, nil
		}
	}

	principals, err := e.expand(ctx, p)
	if err != nil {
		return cachedBindings{}, false, fmt.Errorf("iam: load groups: %w", err)
	}
	list, err := e.store.BindingsFor(ctx, principals)
	if err != nil {
		return cachedBindings{}, false, fmt.Errorf("iam: load bindings: %w", err)
	}
	c := cachedBindings{byResource: make(map[ResourceName][]Binding, len(list)), expires: now.Add(e.cacheTTL)}
	var customNames []string
	for _, b := range list {
		c.byResource[b.Resource] = append(c.byResource[b.Resource], b)
		if IsCustomRoleName(b.Role) {
			customNames = append(customNames, b.Role)
		}
	}
	if cr, ok := e.store.(CustomRoleResolver); ok && len(customNames) > 0 {
		if c.custom, err = cr.CustomRoles(ctx, customNames); err != nil {
			return cachedBindings{}, false, fmt.Errorf("iam: load custom roles: %w", err)
		}
	}
	if e.cacheTTL > 0 {
		e.mu.Lock()
		e.bindings[p] = c
		e.mu.Unlock()
	}
	return c, false, nil
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
// Un groupe (ses bindings sont dans le cache de chacun de ses membres) ou un
// principal spécial (rôle personnalisé modifié) vide tout le cache.
func (e *Engine) Invalidate(p Principal) {
	e.mu.Lock()
	delete(e.bindings, p)
	if p == AllUsers || p == AllAuthenticatedUsers || strings.HasPrefix(string(p), KindGroup+":") {
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

package iam

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/thekrauss/kubemanager/internal/audit"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Policy : vue « policy » des bindings d'une ressource (iam.md §6.2).
type Policy struct {
	Version  int             `json:"version"`
	Etag     string          `json:"etag"`
	Bindings []PolicyBinding `json:"bindings"`
}

type PolicyBinding struct {
	Role      string      `json:"role"`
	Members   []Principal `json:"members"`
	Condition *Condition  `json:"condition,omitempty"`
}

// MemberInfo : affichage d'un membre (la console n'a rien à résoudre).
type MemberInfo struct {
	Principal   Principal `json:"principal"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
}

// PolicyView : policy + informations d'affichage des membres.
type PolicyView struct {
	Resource ResourceName `json:"resource"`
	Policy   Policy       `json:"policy"`
	Members  []MemberInfo `json:"members"`
}

var (
	ErrEtagMismatch       = errors.New("the policy was modified concurrently: reload it and retry")
	ErrEtagRequired       = errors.New("etag is required: read the policy first")
	ErrLastOwner          = errors.New("at least one user must keep roles/owner on this resource")
	ErrUnknownMember      = errors.New("unknown member")
	ErrEscalation         = errors.New("you cannot grant a role with permissions you do not have")
	ErrPolicyNotSupported = errors.New("policies cannot be managed on this resource through the API")
)

// emptyEtag : etag d'une ressource qui n'a encore aucune policy.
const emptyEtag = "ACAB"

// PolicyService : lecture et écriture des policies.
type PolicyService struct {
	DB     *gorm.DB
	Engine *Engine
}

// GetPolicyPermission : permission pour lire la policy de r.
func GetPolicyPermission(r ResourceName) (Permission, error) {
	return policyPermission(r, OrganizationsGetIamPolicy, ProjectsGetIamPolicy)
}

// SetPolicyPermission : permission pour modifier la policy de r.
func SetPolicyPermission(r ResourceName) (Permission, error) {
	return policyPermission(r, OrganizationsSetIamPolicy, ProjectsSetIamPolicy)
}

// Les sous-ressources d'un projet (site, compte de service…) sont gérées avec
// la permission du projet. La policy "platform" ne passe pas par l'API.
func policyPermission(r ResourceName, org, project Permission) (Permission, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	if _, ok := r.Organization(); ok {
		return org, nil
	}
	if _, ok := r.Project(); ok {
		return project, nil
	}
	return "", ErrPolicyNotSupported
}

// Get : policy de r (vide, etag "ACAB", si aucune).
func (s *PolicyService) Get(ctx context.Context, r ResourceName) (*PolicyView, error) {
	if _, err := GetPolicyPermission(r); err != nil {
		return nil, err
	}
	if _, err := s.Engine.Ancestry(ctx, r); err != nil {
		return nil, err // ressource inconnue
	}
	policy, err := loadPolicy(ctx, s.DB, r, false)
	if err != nil {
		return nil, err
	}
	members, err := s.describeMembers(ctx, policy)
	if err != nil {
		return nil, err
	}
	return &PolicyView{Resource: r, Policy: *policy, Members: members}, nil
}

// Set remplace la policy de r. Règles :
//   - etag obligatoire et identique à la policy courante (read-modify-write) ;
//   - membres "user:<uuid>" ou "user:<email>" existants ;
//   - caller ne peut accorder un rôle que s'il en a toutes les permissions sur r ;
//   - une organisation ou un projet garde au moins un owner humain.
func (s *PolicyService) Set(ctx context.Context, caller Principal, r ResourceName, in Policy) (*PolicyView, error) {
	if _, err := SetPolicyPermission(r); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Etag) == "" {
		return nil, ErrEtagRequired
	}
	if _, err := s.Engine.Ancestry(ctx, r); err != nil {
		return nil, err
	}
	bindings, err := s.normalize(ctx, r, in)
	if err != nil {
		return nil, err
	}

	var changed []Principal
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := loadPolicy(ctx, tx, r, true)
		if err != nil {
			return err
		}
		if in.Etag != current.Etag {
			return ErrEtagMismatch
		}
		if err := s.checkNoEscalation(ctx, caller, r, current, bindings); err != nil {
			return err
		}
		if needsOwner(r) && !hasUserOwner(bindings) {
			return ErrLastOwner
		}
		changed = diffPrincipals(current, bindings)
		added, removed := diffBindings(current, bindings)
		audit.Annotate(ctx, "bindings_added", added)
		audit.Annotate(ctx, "bindings_removed", removed)

		if err := tx.Where("resource_name = ?", string(r)).Delete(&bindingRow{}).Error; err != nil {
			return err
		}
		for _, b := range bindings {
			if err := AddBinding(ctx, tx, b, string(caller)); err != nil {
				return err
			}
		}
		return touchPolicy(ctx, tx, r, string(caller))
	})
	if err != nil {
		return nil, err
	}
	for _, p := range changed {
		notifyChanged(p)
	}
	return s.Get(ctx, r)
}

// normalize : valide la policy entrante et la convertit en bindings (membres
// résolus, doublons retirés).
func (s *PolicyService) normalize(ctx context.Context, r ResourceName, in Policy) ([]Binding, error) {
	ancestry, err := s.Engine.Ancestry(ctx, r)
	if err != nil {
		return nil, err
	}
	inScope := map[ResourceName]bool{}
	for _, a := range ancestry {
		inScope[a] = true
	}
	seen := map[string]bool{}
	checkedRoles := map[string]bool{}
	var out []Binding
	for _, pb := range in.Bindings {
		if err := ValidateCondition(pb.Condition); err != nil {
			return nil, fmt.Errorf("role %s: %w", pb.Role, err)
		}
		if IsCustomRoleName(pb.Role) && !checkedRoles[pb.Role] {
			// Rôle personnalisé : existant, et défini sur r ou un de ses parents.
			if _, ok, err := resolveRole(ctx, s.DB, pb.Role); err != nil {
				return nil, err
			} else if !ok {
				return nil, fmt.Errorf("%w: %s", ErrRoleNotFound, pb.Role)
			}
			if parent, _ := CustomRoleParent(pb.Role); !inScope[parent] {
				return nil, fmt.Errorf("%w: %s", ErrCustomRoleOutOfScope, pb.Role)
			}
			checkedRoles[pb.Role] = true
		}
		for _, m := range pb.Members {
			p, err := s.resolveMember(ctx, m)
			if err != nil {
				return nil, err
			}
			if kind, id, _ := p.Parse(); kind == KindGroup {
				org, err := groupOrganization(ctx, s.DB, id)
				if err != nil {
					return nil, err
				}
				if !inScope[OrganizationName(org)] {
					return nil, fmt.Errorf("%w: %s", ErrGroupOutOfScope, p)
				}
			}
			b := Binding{Resource: r, Role: pb.Role, Principal: p, Condition: pb.Condition}
			if err := ValidateBinding(b); err != nil {
				return nil, err
			}
			k := b.Role + "|" + string(p) + "|" + conditionKey(b.Condition)
			if !seen[k] {
				seen[k] = true
				out = append(out, b)
			}
		}
	}
	return out, nil
}

// resolveMember : "user:<email>" → "user:<uuid>" ; vérifie que le membre existe.
func (s *PolicyService) resolveMember(ctx context.Context, m Principal) (Principal, error) {
	return resolveMember(ctx, s.DB, m)
}

func resolveMember(ctx context.Context, db *gorm.DB, m Principal) (Principal, error) {
	if m == AllUsers || m == AllAuthenticatedUsers {
		return m, nil
	}
	kind, raw, ok := strings.Cut(string(m), ":")
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrInvalidPrincipal, m)
	}
	switch kind {
	case KindUser:
		var id uuid.UUID
		q := db.WithContext(ctx).Raw(`SELECT id FROM users WHERE id::text = ? AND deleted_at IS NULL`, raw)
		if strings.Contains(raw, "@") {
			q = db.WithContext(ctx).Raw(`SELECT id FROM users WHERE lower(email) = lower(?) AND deleted_at IS NULL`, raw)
		}
		if err := q.Row().Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("%w: %s", ErrUnknownMember, m)
			}
			return "", err
		}
		return UserPrincipal(id), nil
	case KindServiceAccount, KindGroup:
		id, err := uuid.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("%w: %s", ErrInvalidPrincipal, m)
		}
		table := "service_accounts"
		if kind == KindGroup {
			table = "iam_groups"
		}
		var n int64
		if err := db.WithContext(ctx).Raw(`SELECT count(*) FROM `+table+` WHERE id = ? AND deleted_at IS NULL`, id).
			Row().Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return "", fmt.Errorf("%w: %s", ErrUnknownMember, m)
		}
		return Principal(kind + ":" + id.String()), nil
	default:
		return "", fmt.Errorf("%w: %s", ErrInvalidPrincipal, m)
	}
}

// checkNoEscalation : chaque binding ajouté doit porter un rôle dont le
// caller détient toutes les permissions sur r. Retirer un binding n'est pas
// concerné (la permission setIamPolicy suffit).
func (s *PolicyService) checkNoEscalation(ctx context.Context, caller Principal, r ResourceName, current *Policy, next []Binding) error {
	existing := map[string]bool{}
	for _, pb := range current.Bindings {
		for _, m := range pb.Members {
			existing[pb.Role+"|"+string(m)+"|"+conditionKey(pb.Condition)] = true
		}
	}
	checked := map[string]bool{}
	for _, b := range next {
		if existing[b.Role+"|"+string(b.Principal)+"|"+conditionKey(b.Condition)] || checked[b.Role] {
			continue
		}
		checked[b.Role] = true
		role, _, err := resolveRole(ctx, s.DB, b.Role)
		if err != nil {
			return err
		}
		granted, err := s.Engine.TestPermissions(ctx, caller, r, role.Permissions)
		if err != nil {
			return err
		}
		if len(granted) != len(role.Permissions) {
			return fmt.Errorf("%w: %s", ErrEscalation, b.Role)
		}
	}
	return nil
}

func needsOwner(r ResourceName) bool {
	if _, ok := r.Organization(); ok && !strings.Contains(strings.TrimPrefix(string(r), OrganizationsPrefix), "/") {
		return true
	}
	if _, ok := r.Project(); ok && !strings.Contains(strings.TrimPrefix(string(r), ProjectsPrefix), "/") {
		return true
	}
	return false
}

func hasUserOwner(bs []Binding) bool {
	for _, b := range bs {
		if b.Role == RoleOwner && b.Condition == nil && strings.HasPrefix(string(b.Principal), KindUser+PrincipalSeparator) {
			return true
		}
	}
	return false
}

func diffPrincipals(current *Policy, next []Binding) []Principal {
	set := map[Principal]bool{}
	for _, pb := range current.Bindings {
		for _, m := range pb.Members {
			set[m] = true
		}
	}
	for _, b := range next {
		set[b.Principal] = true
	}
	out := make([]Principal, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	return out
}

// diffBindings : "rôle membre" ajoutés et retirés, pour le journal d'audit.
func diffBindings(current *Policy, next []Binding) (added, removed []string) {
	before := map[string]bool{}
	for _, pb := range current.Bindings {
		for _, m := range pb.Members {
			before[pb.Role+" "+string(m)+conditionSuffix(pb.Condition)] = true
		}
	}
	after := map[string]bool{}
	for _, b := range next {
		k := b.Role + " " + string(b.Principal) + conditionSuffix(b.Condition)
		after[k] = true
		if !before[k] {
			added = append(added, k)
		}
	}
	for k := range before {
		if !after[k] {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func conditionSuffix(c *Condition) string {
	if c == nil {
		return ""
	}
	return " (if " + c.Expression + ")"
}

func conditionKey(c *Condition) string {
	if c == nil {
		return ""
	}
	return c.Title + "\x00" + c.Expression
}

// loadPolicy : bindings de r regroupés par (rôle, condition), triés de façon stable.
func loadPolicy(ctx context.Context, db *gorm.DB, r ResourceName, forUpdate bool) (*Policy, error) {
	q := db.WithContext(ctx)
	var pol policyRow
	pq := q.Where("resource_name = ?", string(r))
	if forUpdate {
		pq = pq.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := pq.First(&pol).Error
	policy := &Policy{Version: 1, Etag: emptyEtag, Bindings: []PolicyBinding{}}
	switch {
	case err == nil:
		policy.Etag, policy.Version = pol.Etag, pol.Version
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}

	var rows []bindingRow
	if err := q.Where("resource_name = ?", string(r)).Find(&rows).Error; err != nil {
		return nil, err
	}
	decoded, err := decodeBindings(rows)
	if err != nil {
		return nil, err
	}
	groups := map[string]*PolicyBinding{}
	var keys []string
	for _, b := range decoded {
		k := b.Role + "|" + conditionKey(b.Condition)
		g, ok := groups[k]
		if !ok {
			g = &PolicyBinding{Role: b.Role, Condition: b.Condition}
			groups[k] = g
			keys = append(keys, k)
		}
		g.Members = append(g.Members, b.Principal)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		sort.Slice(g.Members, func(i, j int) bool { return g.Members[i] < g.Members[j] })
		policy.Bindings = append(policy.Bindings, *g)
	}
	return policy, nil
}

func (s *PolicyService) describeMembers(ctx context.Context, p *Policy) ([]MemberInfo, error) {
	byKind := map[string][]uuid.UUID{}
	seen := map[Principal]bool{}
	for _, b := range p.Bindings {
		for _, m := range b.Members {
			if kind, id, err := m.Parse(); err == nil && id != uuid.Nil && !seen[m] {
				seen[m] = true
				byKind[kind] = append(byKind[kind], id)
			}
		}
	}
	queries := map[string]string{
		KindUser:           `SELECT id, email, full_name FROM users WHERE id IN ?`,
		KindServiceAccount: `SELECT id, '' AS email, COALESCE(NULLIF(display_name, ''), account_id) FROM service_accounts WHERE id IN ?`,
		KindGroup:          `SELECT id, group_id AS email, COALESCE(NULLIF(display_name, ''), group_id) FROM iam_groups WHERE id IN ?`,
	}
	out := []MemberInfo{}
	for kind, ids := range byKind {
		q, ok := queries[kind]
		if !ok {
			continue
		}
		rows, err := s.DB.WithContext(ctx).Raw(q, ids).Rows()
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			var email, name string
			if err := rows.Scan(&id, &email, &name); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, MemberInfo{Principal: Principal(kind + ":" + id.String()), Email: email, DisplayName: name})
		}
		// Connexion coupée en cours de lecture : pas de liste de membres tronquée.
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Principal < out[j].Principal })
	return out, nil
}

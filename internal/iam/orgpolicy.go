package iam

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/thekrauss/kubemanager/internal/audit"
)

// Politiques d'organisation (iam.md §13) : contraintes booléennes posées sur
// une organisation et valables pour tous ses projets.

// ConstraintRequireMFA : toute personne qui agit sur l'organisation ou ses
// projets doit s'être authentifiée avec double authentification. Les comptes
// de service ne sont pas concernés (leur preuve est leur clé).
const ConstraintRequireMFA = "iam.requireMfa"

// orgPolicyTTL : délai maximal de prise d'effet d'un changement sur les autres réplicas.
const orgPolicyTTL = 30 * time.Second

var ErrMFANotConfigured = errors.New("two-factor authentication is not configured on this platform")

type orgPolicyRow struct {
	ResourceName   string `gorm:"primaryKey"`
	ConstraintName string `gorm:"primaryKey"`
	Enforced       bool
	UpdatedBy      string
	UpdatedAt      time.Time
}

func (orgPolicyRow) TableName() string { return "iam_org_policies" }

// OrgSecurityPolicy : vue API des contraintes de sécurité d'une organisation.
type OrgSecurityPolicy struct {
	Organization ResourceName `json:"organization"`
	RequireMFA   bool         `json:"require_mfa"`
	// MFAAvailable : la plateforme sait vérifier la double authentification.
	MFAAvailable bool       `json:"mfa_available"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

// OrgPolicyService : lecture (avec cache court) et écriture des contraintes.
type OrgPolicyService struct {
	DB     *gorm.DB
	Engine *Engine
	// MFAAvailable : faux si la plateforme n'a pas de niveau MFA configuré.
	MFAAvailable bool
	now          func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]orgPolicyEntry
}

type orgPolicyEntry struct {
	requireMFA bool
	at         time.Time
}

func NewOrgPolicyService(db *gorm.DB, engine *Engine, mfaAvailable bool) *OrgPolicyService {
	return &OrgPolicyService{DB: db, Engine: engine, MFAAvailable: mfaAvailable, now: time.Now, cache: map[uuid.UUID]orgPolicyEntry{}}
}

// RequiresMFA : l'organisation de r exige-t-elle la double authentification ?
func (s *OrgPolicyService) RequiresMFA(ctx context.Context, r ResourceName) (bool, error) {
	if !s.MFAAvailable {
		return false, nil
	}
	ancestry, err := s.Engine.Ancestry(ctx, r)
	if err != nil {
		return false, err
	}
	for _, a := range ancestry {
		if org, ok := a.Organization(); ok {
			return s.requireMFA(ctx, org)
		}
	}
	return false, nil
}

func (s *OrgPolicyService) requireMFA(ctx context.Context, org uuid.UUID) (bool, error) {
	now := s.now()
	s.mu.Lock()
	e, ok := s.cache[org]
	s.mu.Unlock()
	if ok && now.Sub(e.at) < orgPolicyTTL {
		return e.requireMFA, nil
	}
	var rows []orgPolicyRow
	if err := s.DB.WithContext(ctx).Where("resource_name = ? AND constraint_name = ?",
		string(OrganizationName(org)), ConstraintRequireMFA).Find(&rows).Error; err != nil {
		return false, err
	}
	enforced := len(rows) == 1 && rows[0].Enforced
	s.mu.Lock()
	s.cache[org] = orgPolicyEntry{requireMFA: enforced, at: now}
	s.mu.Unlock()
	return enforced, nil
}

func (s *OrgPolicyService) Get(ctx context.Context, org uuid.UUID) (*OrgSecurityPolicy, error) {
	var rows []orgPolicyRow
	if err := s.DB.WithContext(ctx).Where("resource_name = ? AND constraint_name = ?",
		string(OrganizationName(org)), ConstraintRequireMFA).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := &OrgSecurityPolicy{Organization: OrganizationName(org), MFAAvailable: s.MFAAvailable}
	if len(rows) == 1 {
		out.RequireMFA = rows[0].Enforced && s.MFAAvailable
		out.UpdatedAt = &rows[0].UpdatedAt
	}
	return out, nil
}

// SetRequireMFA : l'appelant passe par le step-up (permission sensible) :
// il ne peut donc pas s'exclure lui-même en l'activant.
func (s *OrgPolicyService) SetRequireMFA(ctx context.Context, caller Principal, org uuid.UUID, enforced bool) (*OrgSecurityPolicy, error) {
	if enforced && !s.MFAAvailable {
		return nil, ErrMFANotConfigured
	}
	row := orgPolicyRow{ResourceName: string(OrganizationName(org)), ConstraintName: ConstraintRequireMFA,
		Enforced: enforced, UpdatedBy: string(caller), UpdatedAt: s.now()}
	if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.cache, org)
	s.mu.Unlock()
	audit.Annotate(ctx, "constraint", ConstraintRequireMFA)
	audit.Annotate(ctx, "enforced", enforced)
	return s.Get(ctx, org)
}

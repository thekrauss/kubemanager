package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/audit"
)

// Fédération d'identité OIDC (iam.md §7.2) : une relation de confiance dit
// « les jetons de cet émetteur dont le sujet correspond à ce motif (et dont
// les claims valent ces valeurs) peuvent agir comme ce compte de service ».
// Le STS échange alors le jeton de la CI contre un jeton betorun de 15 min
// (RFC 8693) : aucun secret n'est stocké dans la CI.
//
// Émetteurs publics multi-clients (GitHub, gitlab.com) : le motif doit fixer
// le propriétaire (organisation GitHub, groupe GitLab), sinon n'importe quel
// dépôt du monde pourrait agir comme le compte.

const (
	IssuerGitHubActions = "https://token.actions.githubusercontent.com"
	IssuerGitLab        = "https://gitlab.com"

	MaxTrustsPerAccount = 20
	maxTrustConditions  = 10
)

var (
	ErrInvalidIssuer         = errors.New("issuer must be an https URL without query or fragment")
	ErrInvalidSubjectPattern = errors.New("subject_pattern must be an exact subject or a prefix ending with '*' (a single trailing '*')")
	ErrSubjectTooBroad       = errors.New("for this issuer, subject_pattern must pin the owner (GitHub: \"repo:<owner>/…\", GitLab: \"project_path:<group>/…\")")
	ErrInvalidConditions     = errors.New("conditions: at most 10 claims, non-empty names and values")
	ErrTooManyTrusts         = errors.New("this service account already has 20 federation trusts")
	ErrTrustNotFound         = errors.New("federation trust not found")
)

var (
	githubOwnerPinned = regexp.MustCompile(`^repo:[A-Za-z0-9_.-]+/`)
	gitlabOwnerPinned = regexp.MustCompile(`^project_path:[A-Za-z0-9_.-]+/`)
)

// FederationTrust : relation de confiance d'un compte de service.
type FederationTrust struct {
	ID               uuid.UUID         `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	ServiceAccountID uuid.UUID         `gorm:"type:uuid" json:"service_account_id"`
	DisplayName      string            `json:"display_name"`
	Issuer           string            `json:"issuer"`
	SubjectPattern   string            `json:"subject_pattern"`
	ConditionsRaw    []byte            `gorm:"column:conditions;type:jsonb" json:"-"`
	Conditions       map[string]string `gorm:"-" json:"conditions"`
	LastUsedAt       *time.Time        `json:"last_used_at,omitempty"`
	CreatedBy        string            `json:"-"`
	CreatedAt        time.Time         `json:"created_at"`
}

func (FederationTrust) TableName() string { return "iam_federation_trusts" }

func (t *FederationTrust) decode() {
	t.Conditions = map[string]string{}
	_ = json.Unmarshal(t.ConditionsRaw, &t.Conditions)
}

// Matches : le sujet et les claims d'un jeton satisfont-ils la relation ?
func (t *FederationTrust) Matches(subject string, claims map[string]any) bool {
	if p, ok := strings.CutSuffix(t.SubjectPattern, "*"); ok {
		if !strings.HasPrefix(subject, p) {
			return false
		}
	} else if subject != t.SubjectPattern {
		return false
	}
	for name, want := range t.Conditions {
		got, ok := claims[name]
		if !ok || claimString(got) != want {
			return false
		}
	}
	return true
}

func claimString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// FederationTrustInput : création d'une relation de confiance.
type FederationTrustInput struct {
	DisplayName    string            `json:"display_name"`
	Issuer         string            `json:"issuer" desc:"Émetteur OIDC (https://token.actions.githubusercontent.com, https://gitlab.com, ou GitLab auto-hébergé)"`
	SubjectPattern string            `json:"subject_pattern" desc:"Sujet exact ou préfixe terminé par '*' (ex: repo:sunupay/api:ref:refs/heads/main)"`
	Conditions     map[string]string `json:"conditions" desc:"Claims qui doivent valoir exactement ces valeurs (ex: repository_owner_id)"`
}

// NormalizeIssuer : URL https canonique (sans « / » final).
func NormalizeIssuer(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", ErrInvalidIssuer
	}
	return raw, nil
}

func validateTrust(in FederationTrustInput) (string, error) {
	issuer, err := NormalizeIssuer(in.Issuer)
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(in.SubjectPattern)
	if p == "" || len(p) > 255 || strings.Contains(strings.TrimSuffix(p, "*"), "*") || p == "*" {
		return "", ErrInvalidSubjectPattern
	}
	switch issuer {
	case IssuerGitHubActions:
		if !githubOwnerPinned.MatchString(p) {
			return "", ErrSubjectTooBroad
		}
	case IssuerGitLab:
		if !gitlabOwnerPinned.MatchString(p) {
			return "", ErrSubjectTooBroad
		}
	}
	if len(in.Conditions) > maxTrustConditions {
		return "", ErrInvalidConditions
	}
	for k, v := range in.Conditions {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return "", ErrInvalidConditions
		}
	}
	return issuer, nil
}

// CreateTrust : comme une clé, une relation de confiance permet d'agir au
// nom du compte : actAs exigé.
func (s *ServiceAccountService) CreateTrust(ctx context.Context, caller Principal, projectID, saID uuid.UUID, in FederationTrustInput) (*FederationTrust, error) {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return nil, err
	}
	d, err := s.Engine.Check(ctx, caller, ServiceAccountsActAs, sa.Resource)
	if err != nil {
		return nil, err
	}
	if !d.Allowed {
		return nil, ErrActAsRequired
	}
	issuer, err := validateTrust(in)
	if err != nil {
		return nil, err
	}
	var n int64
	if err := s.DB.WithContext(ctx).Model(&FederationTrust{}).Where("service_account_id = ?", sa.ID).Count(&n).Error; err != nil {
		return nil, err
	}
	if n >= MaxTrustsPerAccount {
		return nil, ErrTooManyTrusts
	}
	conds := in.Conditions
	if conds == nil {
		conds = map[string]string{}
	}
	raw, _ := json.Marshal(conds)
	t := &FederationTrust{ID: uuid.New(), ServiceAccountID: sa.ID, DisplayName: truncate(strings.TrimSpace(in.DisplayName), 100),
		Issuer: issuer, SubjectPattern: strings.TrimSpace(in.SubjectPattern), ConditionsRaw: raw, Conditions: conds,
		CreatedBy: string(caller), CreatedAt: s.now()}
	if err := s.DB.WithContext(ctx).Create(t).Error; err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "federation_trust", t.ID.String())
	audit.Annotate(ctx, "issuer", issuer)
	audit.Annotate(ctx, "subject_pattern", t.SubjectPattern)
	return t, nil
}

func (s *ServiceAccountService) ListTrusts(ctx context.Context, projectID, saID uuid.UUID) ([]FederationTrust, error) {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return nil, err
	}
	var out []FederationTrust
	if err := s.DB.WithContext(ctx).Where("service_account_id = ?", sa.ID).Order("created_at").Find(&out).Error; err != nil {
		return nil, err
	}
	for i := range out {
		out[i].decode()
	}
	return out, nil
}

// DeleteTrust : les jetons déjà émis par cette relation sont refusés sous 30 s.
func (s *ServiceAccountService) DeleteTrust(ctx context.Context, projectID, saID, trustID uuid.UUID) error {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return err
	}
	res := s.DB.WithContext(ctx).Where("id = ? AND service_account_id = ?", trustID, sa.ID).Delete(&FederationTrust{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTrustNotFound
	}
	audit.Annotate(ctx, "federation_trust", trustID.String())
	return nil
}

// TrustsForIssuer : relations de confiance d'un émetteur (comptes actifs).
func TrustsForIssuer(ctx context.Context, db *gorm.DB, issuer string) ([]FederationTrust, error) {
	var out []FederationTrust
	err := db.WithContext(ctx).Raw(`SELECT t.* FROM iam_federation_trusts t
		JOIN service_accounts sa ON sa.id = t.service_account_id
		WHERE t.issuer = ? AND sa.deleted_at IS NULL AND NOT sa.disabled`, issuer).Scan(&out).Error
	for i := range out {
		out[i].decode()
	}
	return out, err
}

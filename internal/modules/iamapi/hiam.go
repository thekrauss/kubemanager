// Package iamapi : API publique de l'IAM (docs/architecture/iam.md §14) :
// policies, test de permissions, catalogue des rôles et permissions.
package iamapi

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/core/i18n"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/iam/sts"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

// maxTestedPermissions : borne d'un appel testPermissions.
const maxTestedPermissions = 100

type Controller struct {
	DB              *gorm.DB
	Policies        *iam.PolicyService
	Engine          *iam.Engine
	ServiceAccounts *iam.ServiceAccountService
	ClusterAgents   *iam.ClusterAgents
	STS             *sts.Service
	CustomRoles     *iam.CustomRoleService
	Groups          *iam.GroupService
	OrgPolicies     *iam.OrgPolicyService
	MFA             MFASettings
}

func NewController(policies *iam.PolicyService, engine *iam.Engine, sa *iam.ServiceAccountService, agents *iam.ClusterAgents, tokens *sts.Service) *Controller {
	return &Controller{DB: policies.DB, Policies: policies, Engine: engine, ServiceAccounts: sa, ClusterAgents: agents, STS: tokens,
		CustomRoles: &iam.CustomRoleService{DB: policies.DB, Engine: engine},
		Groups:      &iam.GroupService{DB: policies.DB, Engine: engine}}
}

type GetPolicyRequest struct {
	Resource string `query:"resource" binding:"required" desc:"Nom de la ressource (ex: projects/<uuid>)"`
}

type SetPolicyRequest struct {
	Resource string     `json:"resource" binding:"required" desc:"Nom de la ressource (ex: projects/<uuid>)"`
	Policy   iam.Policy `json:"policy" binding:"required" desc:"Policy complète, avec l'etag lu par GET /iam/policy"`
}

type TestPermissionsRequest struct {
	Resource    string   `json:"resource" binding:"required"`
	Permissions []string `json:"permissions" binding:"required"`
}

type TestPermissionsResponse struct {
	// Permissions : sous-ensemble des permissions demandées accordé à l'appelant.
	Permissions []string `json:"permissions"`
}

type RoleView struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

type PermissionView struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	CustomRoles bool   `json:"custom_roles"`
}

func (h *Controller) GetPolicy(c *gin.Context, in *GetPolicyRequest) (*iam.PolicyView, error) {
	return h.Policies.Get(c.Request.Context(), iam.ResourceName(in.Resource))
}

func (h *Controller) SetPolicy(c *gin.Context, in *SetPolicyRequest) (*iam.PolicyView, error) {
	caller, ok := security.PrincipalFromContext(c)
	if !ok {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "authentication required")
	}
	return h.Policies.Set(c.Request.Context(), caller, iam.ResourceName(in.Resource), in.Policy)
}

// TestPermissions : la console l'appelle pour afficher ou masquer les actions.
// N'expose que les droits de l'appelant lui-même.
func (h *Controller) TestPermissions(c *gin.Context, in *TestPermissionsRequest) (*TestPermissionsResponse, error) {
	caller, ok := security.PrincipalFromContext(c)
	if !ok {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "authentication required")
	}
	if len(in.Permissions) > maxTestedPermissions {
		return nil, betoerrors.Newf(betoerrors.CodeInvalidInput, "at most %d permissions per call", maxTestedPermissions)
	}
	perms := make([]iam.Permission, 0, len(in.Permissions))
	for _, p := range in.Permissions {
		if !iam.Known(iam.Permission(p)) {
			return nil, betoerrors.Newf(betoerrors.CodeInvalidInput, "unknown permission %q", p)
		}
		perms = append(perms, iam.Permission(p))
	}
	granted, err := h.Engine.TestPermissions(c.Request.Context(), caller, iam.ResourceName(in.Resource), perms)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(granted))
	for i, p := range granted {
		out[i] = p.String()
	}
	return &TestPermissionsResponse{Permissions: out}, nil
}

// ListRoles : titres et descriptions dans la langue de la requête.
func (h *Controller) ListRoles(c *gin.Context) ([]RoleView, error) {
	locale := i18n.From(c.Request.Context())
	roles := iam.PredefinedRoles()
	out := make([]RoleView, 0, len(roles))
	for _, r := range roles {
		perms := make([]string, len(r.Permissions))
		for i, p := range r.Permissions {
			perms[i] = p.String()
		}
		out = append(out, RoleView{Name: r.Name, Title: i18n.Translate(locale, r.Title),
			Description: i18n.Translate(locale, r.Description), Permissions: perms})
	}
	return out, nil
}

func (h *Controller) ListPermissions(c *gin.Context) ([]PermissionView, error) {
	locale := i18n.From(c.Request.Context())
	var out []PermissionView
	for _, p := range iam.AllPermissions() {
		info, _ := iam.Info(p)
		if info.PlatformOnly {
			continue
		}
		out = append(out, PermissionView{Name: p.String(), Title: i18n.Translate(locale, info.Title), CustomRoles: info.CustomRoles})
	}
	return out, nil
}

// ── Comptes de service (/projects/:id/serviceAccounts) ───────────────────

type ProjectPath struct {
	ProjectID string `path:"id" binding:"required"`
}

type ServiceAccountPath struct {
	ProjectID string `path:"id" binding:"required"`
	SAID      string `path:"saID" binding:"required"`
}

type APIKeyPath struct {
	ProjectID string `path:"id" binding:"required"`
	SAID      string `path:"saID" binding:"required"`
	KeyID     string `path:"keyID" binding:"required" desc:"Identifiant public de la clé (key_id)"`
}

type CreateServiceAccountRequest struct {
	ProjectID   string `path:"id" binding:"required"`
	AccountID   string `json:"account_id" binding:"required" desc:"6-30 caractères : minuscules, chiffres, tirets (ex: ci-deployer)"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type CreateAPIKeyRequest struct {
	ProjectID     string              `path:"id" binding:"required"`
	SAID          string              `path:"saID" binding:"required"`
	DisplayName   string              `json:"display_name"`
	Env           string              `json:"env" desc:"live (défaut) ou test"`
	ExpiresInDays int                 `json:"expires_in_days" desc:"1 à 90 jours (défaut 90)"`
	Restrictions  iam.KeyRestrictions `json:"restrictions" desc:"Permissions, ressources et plages IP autorisées (toutes optionnelles)"`
}

func ids(raw ...string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, len(raw))
	for i, r := range raw {
		id, err := uuid.Parse(r)
		if err != nil {
			return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid id")
		}
		out[i] = id
	}
	return out, nil
}

func caller(c *gin.Context) (iam.Principal, error) {
	p, ok := security.PrincipalFromContext(c)
	if !ok {
		return "", betoerrors.New(betoerrors.CodeUnauthorized, "authentication required")
	}
	return p, nil
}

func (h *Controller) CreateServiceAccount(c *gin.Context, in *CreateServiceAccountRequest) (*iam.ServiceAccount, error) {
	p, err := caller(c)
	if err != nil {
		return nil, err
	}
	id, err := ids(in.ProjectID)
	if err != nil {
		return nil, err
	}
	return h.ServiceAccounts.Create(c.Request.Context(), p, id[0], in.AccountID, in.DisplayName, in.Description)
}

func (h *Controller) ListServiceAccounts(c *gin.Context, in *ProjectPath) ([]iam.ServiceAccount, error) {
	id, err := ids(in.ProjectID)
	if err != nil {
		return nil, err
	}
	return h.ServiceAccounts.List(c.Request.Context(), id[0])
}

func (h *Controller) GetServiceAccount(c *gin.Context, in *ServiceAccountPath) (*iam.ServiceAccount, error) {
	id, err := ids(in.ProjectID, in.SAID)
	if err != nil {
		return nil, err
	}
	return h.ServiceAccounts.Get(c.Request.Context(), id[0], id[1])
}

func (h *Controller) DeleteServiceAccount(c *gin.Context, in *ServiceAccountPath) error {
	p, err := caller(c)
	if err != nil {
		return err
	}
	id, err := ids(in.ProjectID, in.SAID)
	if err != nil {
		return err
	}
	return h.ServiceAccounts.Delete(c.Request.Context(), p, id[0], id[1])
}

// CreateAPIKey : le secret n'apparaît que dans cette réponse.
func (h *Controller) CreateAPIKey(c *gin.Context, in *CreateAPIKeyRequest) (*iam.CreatedAPIKey, error) {
	p, err := caller(c)
	if err != nil {
		return nil, err
	}
	id, err := ids(in.ProjectID, in.SAID)
	if err != nil {
		return nil, err
	}
	return h.ServiceAccounts.CreateKey(c.Request.Context(), p, id[0], id[1], iam.CreateKeyRequest{
		DisplayName: in.DisplayName, Env: in.Env, ExpiresInDays: in.ExpiresInDays, Restrictions: in.Restrictions,
	})
}

func (h *Controller) ListAPIKeys(c *gin.Context, in *ServiceAccountPath) ([]iam.APIKey, error) {
	id, err := ids(in.ProjectID, in.SAID)
	if err != nil {
		return nil, err
	}
	return h.ServiceAccounts.ListKeys(c.Request.Context(), id[0], id[1])
}

func (h *Controller) RevokeAPIKey(c *gin.Context, in *APIKeyPath) error {
	id, err := ids(in.ProjectID, in.SAID)
	if err != nil {
		return err
	}
	return h.ServiceAccounts.RevokeKey(c.Request.Context(), id[0], id[1], in.KeyID)
}

// ── Journal d'audit ───────────────────────────────────────────────────────

type AuditLogsRequest struct {
	Resource string `query:"resource" binding:"required" desc:"organizations/<uuid>, projects/<uuid> ou une sous-ressource"`
	BeforeID int64  `query:"before_id" desc:"Curseur : entrées plus anciennes que cet id"`
	Limit    int    `query:"limit" desc:"1 à 200 (défaut 50)"`
}

type AuditLogsResponse struct {
	Entries []audit.Entry `json:"entries"`
	// NextBeforeID : à repasser en before_id pour la page suivante (0 = fin).
	NextBeforeID int64 `json:"next_before_id"`
}

func (h *Controller) ListAuditLogs(c *gin.Context, in *AuditLogsRequest) (*AuditLogsResponse, error) {
	r := iam.ResourceName(in.Resource)
	q := audit.Query{Resource: string(r), BeforeID: in.BeforeID, Limit: in.Limit}
	if org, ok := r.Organization(); ok && !strings.Contains(strings.TrimPrefix(string(r), "organizations/"), "/") {
		q.OrganizationID = &org
	} else if project, ok := r.Project(); ok && !strings.Contains(strings.TrimPrefix(string(r), "projects/"), "/") {
		q.ProjectID = &project
	}
	entries, err := audit.List(c.Request.Context(), h.DB, q)
	if err != nil {
		return nil, err
	}
	resp := &AuditLogsResponse{Entries: entries}
	limit := in.Limit
	if limit <= 0 || limit > audit.MaxPageSize {
		limit = 50
	}
	if len(entries) == limit {
		resp.NextBeforeID = entries[len(entries)-1].ID
	}
	return resp, nil
}

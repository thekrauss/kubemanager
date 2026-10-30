package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Types de l'IAM (groupes, rôles personnalisés, comptes de service). Locaux
// pour la même raison que la policy : ceux du serveur tirent la base.
type (
	Group struct {
		ID             string    `json:"id"`
		OrganizationID string    `json:"organization_id"`
		GroupID        string    `json:"group_id"`
		DisplayName    string    `json:"display_name"`
		Description    string    `json:"description"`
		Principal      string    `json:"principal"`
		MemberCount    int       `json:"member_count"`
		CreatedAt      time.Time `json:"created_at"`
	}
	GroupMember struct {
		Principal   string    `json:"principal"`
		Email       string    `json:"email,omitempty"`
		DisplayName string    `json:"display_name,omitempty"`
		AddedAt     time.Time `json:"added_at"`
	}
	CustomRole struct {
		Name        string   `json:"name"`
		Parent      string   `json:"parent"`
		RoleID      string   `json:"role_id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
		Etag        string   `json:"etag"`
	}
	CustomRoleInput struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	Permission struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		CustomRoles bool   `json:"custom_roles"`
	}
	ServiceAccount struct {
		ID          string    `json:"id"`
		ProjectID   string    `json:"project_id"`
		AccountID   string    `json:"account_id"`
		DisplayName string    `json:"display_name"`
		Description string    `json:"description"`
		Disabled    bool      `json:"disabled"`
		Principal   string    `json:"principal"`
		CreatedAt   time.Time `json:"created_at"`
	}
	ServiceAccountKey struct {
		ID          string     `json:"id"`
		KeyID       string     `json:"key_id"`
		Algorithm   string     `json:"algorithm"`
		Origin      string     `json:"origin"`
		DisplayName string     `json:"display_name"`
		ValidBefore time.Time  `json:"valid_before"`
		DisabledAt  *time.Time `json:"disabled_at,omitempty"`
		LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
		CreatedAt   time.Time  `json:"created_at"`
	}
	CreatedServiceAccountKey struct {
		ServiceAccountKey
		KeyFile *KeyFile `json:"key_file,omitempty"`
	}
	FederationTrust struct {
		ID             string            `json:"id"`
		DisplayName    string            `json:"display_name"`
		Issuer         string            `json:"issuer"`
		SubjectPattern string            `json:"subject_pattern"`
		Conditions     map[string]string `json:"conditions"`
		LastUsedAt     *time.Time        `json:"last_used_at,omitempty"`
		CreatedAt      time.Time         `json:"created_at"`
	}
	FederationTrustInput struct {
		DisplayName    string            `json:"display_name"`
		Issuer         string            `json:"issuer"`
		SubjectPattern string            `json:"subject_pattern"`
		Conditions     map[string]string `json:"conditions,omitempty"`
	}
)

// Ancestry : ascendance d'une ressource (projet → organisation → platform).
func (c *Client) Ancestry(ctx context.Context, resource string) ([]string, error) {
	var out struct {
		Ancestors []string `json:"ancestors"`
	}
	return out.Ancestors, c.Do(ctx, http.MethodGet, pathIAMAncestry+"?"+queryResource+"="+url.QueryEscape(resource), nil, &out)
}

// STSIssuer : émetteur du STS, audience attendue des jetons OIDC des CI.
func (c *Client) STSIssuer(ctx context.Context) (string, error) {
	var meta struct {
		Issuer string `json:"issuer"`
	}
	return meta.Issuer, c.DoPublic(ctx, http.MethodGet, stsDiscoveryPath, nil, &meta)
}

func (c *Client) ListPermissions(ctx context.Context) ([]Permission, error) {
	var out []Permission
	return out, c.Do(ctx, http.MethodGet, pathIAMPermissions, nil, &out)
}

// ── Groupes ──

func orgGroups(org string) string { return pathOrganizations + "/" + url.PathEscape(org) + segGroups }

func (c *Client) ListGroups(ctx context.Context, org string) ([]Group, error) {
	var out []Group
	return out, c.Do(ctx, http.MethodGet, orgGroups(org), nil, &out)
}

func (c *Client) CreateGroup(ctx context.Context, org, groupID, displayName, description string) (*Group, error) {
	var out Group
	body := map[string]string{"group_id": groupID, "display_name": displayName, "description": description}
	return &out, c.Do(ctx, http.MethodPost, orgGroups(org), body, &out)
}

func (c *Client) DeleteGroup(ctx context.Context, org, id string) error {
	return c.Do(ctx, http.MethodDelete, orgGroups(org)+"/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListGroupMembers(ctx context.Context, org, id string) ([]GroupMember, error) {
	var out []GroupMember
	return out, c.Do(ctx, http.MethodGet, orgGroups(org)+"/"+url.PathEscape(id)+segMembers, nil, &out)
}

func (c *Client) AddGroupMember(ctx context.Context, org, id, member string) (*GroupMember, error) {
	var out GroupMember
	return &out, c.Do(ctx, http.MethodPost, orgGroups(org)+"/"+url.PathEscape(id)+segMembers,
		map[string]string{"member": member}, &out)
}

func (c *Client) RemoveGroupMember(ctx context.Context, org, id, member string) error {
	return c.Do(ctx, http.MethodDelete,
		orgGroups(org)+"/"+url.PathEscape(id)+segMembers+"?"+queryMember+"="+url.QueryEscape(member), nil, nil)
}

// ── Rôles personnalisés ──

func (c *Client) ListCustomRoles(ctx context.Context, parent string) ([]CustomRole, error) {
	var out []CustomRole
	return out, c.Do(ctx, http.MethodGet, pathIAMCustomRoles+"?"+queryParent+"="+url.QueryEscape(parent), nil, &out)
}

func (c *Client) GetCustomRole(ctx context.Context, name string) (*CustomRole, error) {
	var out CustomRole
	return &out, c.Do(ctx, http.MethodGet, pathIAMCustomRole+"?"+queryName+"="+url.QueryEscape(name), nil, &out)
}

func (c *Client) CreateCustomRole(ctx context.Context, parent, roleID string, in CustomRoleInput) (*CustomRole, error) {
	var out CustomRole
	body := struct {
		Parent string `json:"parent"`
		RoleID string `json:"role_id"`
		CustomRoleInput
	}{parent, roleID, in}
	return &out, c.Do(ctx, http.MethodPost, pathIAMCustomRoles, body, &out)
}

func (c *Client) UpdateCustomRole(ctx context.Context, name, etag string, in CustomRoleInput) (*CustomRole, error) {
	var out CustomRole
	body := struct {
		Name string `json:"name"`
		Etag string `json:"etag"`
		CustomRoleInput
	}{name, etag, in}
	return &out, c.Do(ctx, http.MethodPatch, pathIAMCustomRole, body, &out)
}

func (c *Client) DeleteCustomRole(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodDelete, pathIAMCustomRole+"?"+queryName+"="+url.QueryEscape(name), nil, nil)
}

// ── Comptes de service ──

func projectSAs(project string) string {
	return pathProjects + "/" + url.PathEscape(project) + segServiceAccounts
}

func (c *Client) ListServiceAccounts(ctx context.Context, project string) ([]ServiceAccount, error) {
	var out []ServiceAccount
	return out, c.Do(ctx, http.MethodGet, projectSAs(project), nil, &out)
}

func (c *Client) CreateServiceAccount(ctx context.Context, project, accountID, displayName, description string) (*ServiceAccount, error) {
	var out ServiceAccount
	body := map[string]string{"account_id": accountID, "display_name": displayName, "description": description}
	return &out, c.Do(ctx, http.MethodPost, projectSAs(project), body, &out)
}

func (c *Client) DeleteServiceAccount(ctx context.Context, project, id string) error {
	return c.Do(ctx, http.MethodDelete, projectSAs(project)+"/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListServiceAccountKeys(ctx context.Context, project, id string) ([]ServiceAccountKey, error) {
	var out []ServiceAccountKey
	return out, c.Do(ctx, http.MethodGet, projectSAs(project)+"/"+url.PathEscape(id)+segKeys, nil, &out)
}

func (c *Client) CreateServiceAccountKey(ctx context.Context, project, id, displayName string, expiresInDays int) (*CreatedServiceAccountKey, error) {
	var out CreatedServiceAccountKey
	body := map[string]any{"display_name": displayName, "expires_in_days": expiresInDays}
	return &out, c.Do(ctx, http.MethodPost, projectSAs(project)+"/"+url.PathEscape(id)+segKeys, body, &out)
}

func (c *Client) DisableServiceAccountKey(ctx context.Context, project, id, keyID string) error {
	return c.Do(ctx, http.MethodDelete, projectSAs(project)+"/"+url.PathEscape(id)+segKeys+"/"+url.PathEscape(keyID), nil, nil)
}

func (c *Client) ListTrusts(ctx context.Context, project, id string) ([]FederationTrust, error) {
	var out []FederationTrust
	return out, c.Do(ctx, http.MethodGet, projectSAs(project)+"/"+url.PathEscape(id)+segFederation, nil, &out)
}

func (c *Client) CreateTrust(ctx context.Context, project, id string, in FederationTrustInput) (*FederationTrust, error) {
	var out FederationTrust
	return &out, c.Do(ctx, http.MethodPost, projectSAs(project)+"/"+url.PathEscape(id)+segFederation, in, &out)
}

func (c *Client) DeleteTrust(ctx context.Context, project, id, trustID string) error {
	return c.Do(ctx, http.MethodDelete, projectSAs(project)+"/"+url.PathEscape(id)+segFederation+"/"+url.PathEscape(trustID), nil, nil)
}

package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
	sitesdomain "github.com/thekrauss/kubemanager/internal/modules/sites/domain"
)

// BootstrapStatus : profil et projet de l'utilisateur (type de l'API).
type BootstrapStatus = identitytypes.BootstrapStatusResponse

func (c *Client) BootstrapStatus(ctx context.Context) (*BootstrapStatus, error) {
	var out BootstrapStatus
	return &out, c.Do(ctx, http.MethodGet, "/identity/bootstrap/status", nil, &out)
}

// Session : état d'authentification (double authentification…).
type Session struct {
	Principal    string `json:"principal"`
	AuthMethod   string `json:"auth_method"`
	MFAAvailable bool   `json:"mfa_available"`
	MFA          bool   `json:"mfa"`
}

func (c *Client) Session(ctx context.Context) (*Session, error) {
	var out Session
	return &out, c.Do(ctx, http.MethodGet, "/iam/session", nil, &out)
}

// Site et Deployment : types de l'API des sites statiques.
type (
	Site       = sitesdomain.SiteResponse
	Deployment = sitesdomain.SiteDeployment
)

func sitesPath(project string) string { return "/projects/" + url.PathEscape(project) + "/sites" }

func (c *Client) ListSites(ctx context.Context, project string) ([]Site, error) {
	var out []Site
	return out, c.Do(ctx, http.MethodGet, sitesPath(project), nil, &out)
}

func (c *Client) CreateSite(ctx context.Context, project, name string, spa bool) (*Site, error) {
	var out Site
	// Corps : seuls les champs JSON de CreateSiteRequest (le projet est dans l'URL).
	body := struct {
		Name        string `json:"name"`
		SPAFallback bool   `json:"spa_fallback"`
	}{name, spa}
	return &out, c.Do(ctx, http.MethodPost, sitesPath(project), body, &out)
}

func (c *Client) GetSite(ctx context.Context, project, siteID string) (*Site, error) {
	var out Site
	return &out, c.Do(ctx, http.MethodGet, sitesPath(project)+"/"+url.PathEscape(siteID), nil, &out)
}

func (c *Client) UploadDeployment(ctx context.Context, project, siteID string, archive io.Reader) (*Deployment, error) {
	var out Deployment
	err := c.Upload(ctx, sitesPath(project)+"/"+url.PathEscape(siteID)+"/deployments", "archive", "site.zip", archive, &out)
	return &out, err
}

func (c *Client) ListDeployments(ctx context.Context, project, siteID string) ([]Deployment, error) {
	var out []Deployment
	return out, c.Do(ctx, http.MethodGet, sitesPath(project)+"/"+url.PathEscape(siteID)+"/deployments", nil, &out)
}

// FindDeployment : état courant d'un déploiement.
func (c *Client) FindDeployment(ctx context.Context, project, siteID, id string) (*Deployment, error) {
	list, err := c.ListDeployments(ctx, project, siteID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID.String() == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("deployment %s not found", id)
}

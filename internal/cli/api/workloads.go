package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"

	workloadsdomain "github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
)

// Types de l'API des conteneurs.
type (
	Workload              = workloadsdomain.Workload
	CreateWorkloadRequest = workloadsdomain.CreateWorkloadRequest
	WorkloadResponse      = workloadsdomain.WorkloadResponse
	UpdateWorkloadRequest = workloadsdomain.UpdateWorkloadRequest
)

func (c *Client) ListWorkloads(ctx context.Context, project string) ([]Workload, error) {
	var out []Workload
	return out, c.Do(ctx, http.MethodGet, pathWorkloads+"?"+queryProjectID+"="+url.QueryEscape(project), nil, &out)
}

func (c *Client) GetWorkload(ctx context.Context, id string) (*Workload, error) {
	var out Workload
	return &out, c.Do(ctx, http.MethodGet, pathWorkloads+"/"+url.PathEscape(id), nil, &out)
}

func (c *Client) CreateWorkload(ctx context.Context, req CreateWorkloadRequest) (*WorkloadResponse, error) {
	var out WorkloadResponse
	return &out, c.Do(ctx, http.MethodPost, pathWorkloads, req, &out)
}

// UpdateWorkload : nouvelle version (image, variables).
func (c *Client) UpdateWorkload(ctx context.Context, id string, req UpdateWorkloadRequest) error {
	return c.Do(ctx, http.MethodPatch, pathWorkloads+"/"+url.PathEscape(id), req, nil)
}

// WorkloadAction : stop, start, restart (corps vide).
func (c *Client) WorkloadAction(ctx context.Context, id, action string) error {
	return c.Do(ctx, http.MethodPost, pathWorkloads+"/"+url.PathEscape(id)+"/"+action, nil, nil)
}

func (c *Client) DeleteWorkload(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, pathWorkloads+"/"+url.PathEscape(id), nil, nil)
}

// LogsOptions : paramètres de GET /workloads/:id/logs.
type LogsOptions struct {
	Follow     bool
	Previous   bool
	Tail       int64
	Since      int64 // secondes
	Timestamps bool
}

// StreamLogs : copie les logs vers w jusqu'à la fin du flux (ou Ctrl-C en mode suivi).
func (c *Client) StreamLogs(ctx context.Context, id string, o LogsOptions, w io.Writer) error {
	q := url.Values{}
	q.Set("follow", strconv.FormatBool(o.Follow))
	q.Set("previous", strconv.FormatBool(o.Previous))
	q.Set("timestamps", strconv.FormatBool(o.Timestamps))
	if o.Tail > 0 {
		q.Set("tail", strconv.FormatInt(o.Tail, 10))
	}
	if o.Since > 0 {
		q.Set("since", strconv.FormatInt(o.Since, 10))
	}
	return c.Stream(ctx, pathWorkloads+"/"+url.PathEscape(id)+segLogs+"?"+q.Encode(), w)
}

// Types des variables d'un conteneur.
type (
	VariableView          = workloadsdomain.VariableView
	VariablesResponse     = workloadsdomain.VariablesResponse
	PatchVariablesRequest = workloadsdomain.PatchVariablesRequest
)

// ListVariables : variables et secrets, sans la valeur des secrets.
func (c *Client) ListVariables(ctx context.Context, id string) (*VariablesResponse, error) {
	var out VariablesResponse
	return &out, c.Do(ctx, http.MethodGet, pathWorkloads+"/"+url.PathEscape(id)+"/variables", nil, &out)
}

// PatchVariables : applique les modifications (redéploiement si une valeur change).
func (c *Client) PatchVariables(ctx context.Context, id string, req PatchVariablesRequest) (*VariablesResponse, error) {
	var out VariablesResponse
	return &out, c.Do(ctx, http.MethodPatch, pathWorkloads+"/"+url.PathEscape(id)+"/variables", req, &out)
}

// Types des domaines d'un conteneur.
type (
	WorkloadDomainView = workloadsdomain.DomainResponse
	AddDomainRequest   = workloadsdomain.AddDomainRequest
)

func workloadDomainsPath(id string) string {
	return pathWorkloads + "/" + url.PathEscape(id) + segDomains
}

// ListWorkloadDomains : domaines et enregistrements DNS à créer.
func (c *Client) ListWorkloadDomains(ctx context.Context, id string) ([]WorkloadDomainView, error) {
	var out []WorkloadDomainView
	return out, c.Do(ctx, http.MethodGet, workloadDomainsPath(id), nil, &out)
}

// AddWorkloadDomain : nouvelle demande.
func (c *Client) AddWorkloadDomain(ctx context.Context, id, host string) (*WorkloadDomainView, error) {
	var out WorkloadDomainView
	return &out, c.Do(ctx, http.MethodPost, workloadDomainsPath(id), AddDomainRequest{Hostname: host}, &out)
}

// VerifyWorkloadDomain : vérifie la preuve de propriété.
func (c *Client) VerifyWorkloadDomain(ctx context.Context, id, domainID string) (*WorkloadDomainView, error) {
	var out WorkloadDomainView
	return &out, c.Do(ctx, http.MethodPost, workloadDomainsPath(id)+"/"+url.PathEscape(domainID)+segVerify, nil, &out)
}

// RemoveWorkloadDomain : retire le domaine et sa route.
func (c *Client) RemoveWorkloadDomain(ctx context.Context, id, domainID string) error {
	return c.Do(ctx, http.MethodDelete, workloadDomainsPath(id)+"/"+url.PathEscape(domainID), nil, nil)
}

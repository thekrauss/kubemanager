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

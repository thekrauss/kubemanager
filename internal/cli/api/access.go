package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

// Policy IAM d'une ressource. Types locaux : ceux du serveur (internal/iam)
// tirent la base de données. La condition est conservée telle quelle pour
// ne rien perdre en réécrivant la policy.
type (
	PolicyBinding struct {
		Role      string          `json:"role"`
		Members   []string        `json:"members"`
		Condition json.RawMessage `json:"condition,omitempty"`
	}
	Policy struct {
		Version  int             `json:"version"`
		Etag     string          `json:"etag"`
		Bindings []PolicyBinding `json:"bindings"`
	}
	PolicyMember struct {
		Principal   string `json:"principal"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	}
	PolicyView struct {
		Resource string         `json:"resource"`
		Policy   Policy         `json:"policy"`
		Members  []PolicyMember `json:"members"`
	}
	Role struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
)

func (c *Client) GetPolicy(ctx context.Context, resource string) (*PolicyView, error) {
	var out PolicyView
	return &out, c.Do(ctx, http.MethodGet, "/iam/policy?resource="+url.QueryEscape(resource), nil, &out)
}

func (c *Client) SetPolicy(ctx context.Context, resource string, p Policy) (*PolicyView, error) {
	var out PolicyView
	body := struct {
		Resource string `json:"resource"`
		Policy   Policy `json:"policy"`
	}{resource, p}
	return &out, c.Do(ctx, http.MethodPost, "/iam/policy", body, &out)
}

func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	var out []Role
	return out, c.Do(ctx, http.MethodGet, "/iam/roles", nil, &out)
}

// UpdatePolicy : lecture, modification, écriture avec l'etag ; relit et
// recommence si la policy a changé entre-temps (409 ABORTED).
func (c *Client) UpdatePolicy(ctx context.Context, resource string, edit func(*PolicyView) error) (*PolicyView, error) {
	for attempt := 0; ; attempt++ {
		view, err := c.GetPolicy(ctx, resource)
		if err != nil {
			return nil, err
		}
		if err := edit(view); err != nil {
			return nil, err
		}
		out, err := c.SetPolicy(ctx, resource, view.Policy)
		var apiErr *Error
		if attempt < 3 && errors.As(err, &apiErr) && apiErr.Code == "ABORTED" {
			continue
		}
		return out, err
	}
}

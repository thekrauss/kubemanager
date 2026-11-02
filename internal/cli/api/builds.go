package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"

	buildsdomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
)

// Types de l'API des builds.
type (
	Build                 = buildsdomain.Build
	CreateGitBuildRequest = buildsdomain.CreateGitBuildRequest
)

const uploadSourceName = "source.tar.gz"

func buildsPath(project string) string {
	return pathProjects + "/" + url.PathEscape(project) + "/builds"
}

// UploadBuild : construit une archive .tar.gz.
func (c *Client) UploadBuild(ctx context.Context, project, name, dockerfile string, archive io.Reader) (*Build, error) {
	var out Build
	fields := map[string]string{"name": name, "dockerfile": dockerfile}
	err := c.UploadWithFields(ctx, buildsPath(project)+"/upload", fields, buildsdomain.ArchiveFormField, uploadSourceName, archive, &out)
	return &out, err
}

// CreateGitBuild : construit un dépôt public.
func (c *Client) CreateGitBuild(ctx context.Context, project string, req CreateGitBuildRequest) (*Build, error) {
	var out Build
	return &out, c.Do(ctx, http.MethodPost, buildsPath(project), req, &out)
}

// GetBuild : état d'un build.
func (c *Client) GetBuild(ctx context.Context, project, id string) (*Build, error) {
	var out Build
	return &out, c.Do(ctx, http.MethodGet, buildsPath(project)+"/"+url.PathEscape(id), nil, &out)
}

// ListBuilds : builds récents.
func (c *Client) ListBuilds(ctx context.Context, project, name string) ([]Build, error) {
	var out []Build
	path := buildsPath(project)
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	return out, c.Do(ctx, http.MethodGet, path, nil, &out)
}

// CancelBuild : arrête un build en cours.
func (c *Client) CancelBuild(ctx context.Context, project, id string) error {
	return c.Do(ctx, http.MethodPost, buildsPath(project)+"/"+url.PathEscape(id)+"/cancel", nil, nil)
}

// StreamBuildLogs : journal copié vers w ; follow : jusqu'à la fin du build.
func (c *Client) StreamBuildLogs(ctx context.Context, project, id string, follow bool, w io.Writer) error {
	return c.Stream(ctx, buildsPath(project)+"/"+url.PathEscape(id)+segLogs+"?follow="+strconv.FormatBool(follow), w)
}

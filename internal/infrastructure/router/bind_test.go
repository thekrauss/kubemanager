package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/loopfz/gadgeto/tonic"
	"github.com/stretchr/testify/assert"
)

type embeddedBody struct {
	Title string `json:"title" binding:"required"`
}

type createInProject struct {
	ProjectID string `path:"id" binding:"required"`
	Filter    string `query:"filter"`
	Name      string `json:"name" binding:"required"`
	embeddedBody
}

// Paramètre de chemin obligatoire + corps JSON : accepté (avant : 400
// « ProjectID : champ obligatoire » sur toutes les routes de ce type).
func TestBindHook_PathParamsBeforeBodyValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tonic.SetBindHook(bindHook(tonic.DefaultMaxBodyBytes))
	defer tonic.SetBindHook(tonic.DefaultBindingHook)
	var got createInProject
	r := gin.New()
	r.POST("/projects/:id/things", tonic.Handler(func(_ *gin.Context, in *createInProject) (*createInProject, error) {
		got = *in
		return in, nil
	}, http.StatusCreated))
	call := func(body string) int {
		req := httptest.NewRequest("POST", "/projects/p-1/things?filter=f", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusCreated, call(`{"name":"vitrine","title":"t"}`))
	assert.Equal(t, "p-1", got.ProjectID)
	assert.Equal(t, "f", got.Filter)
	assert.Equal(t, "vitrine", got.Name)
	assert.Equal(t, "t", got.Title)

	// Les vraies règles restent appliquées.
	assert.Equal(t, http.StatusBadRequest, call(`{"title":"t"}`), "name absent")
	assert.Equal(t, http.StatusBadRequest, call(`{"name":"x"}`), "champ obligatoire d'une structure imbriquée")
}

type rawLogsInput struct {
	ID     string `path:"id" binding:"required"`
	Follow bool   `query:"follow"`
	Tail   int64  `query:"tail"`
}

// Handlers bruts (logs en flux) : mêmes étiquettes path:/query: que tonic.
// Avant : « ID: required field » sur GET /workloads/:id/logs.
func TestWrapRawHandler_ReadsPathAndQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got rawLogsInput
	r := gin.New()
	r.GET("/workloads/:id/logs", wrapRawHandler(func(c *gin.Context, in *rawLogsInput) {
		got = *in
		c.Status(http.StatusOK)
	}))
	call := func(url string) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, call("/workloads/w-1/logs?follow=true&tail=50"))
	assert.Equal(t, rawLogsInput{ID: "w-1", Follow: true, Tail: 50}, got)
	assert.Equal(t, http.StatusOK, call("/workloads/w-2/logs"))
	assert.Equal(t, rawLogsInput{ID: "w-2"}, got)
	assert.Equal(t, http.StatusBadRequest, call("/workloads/w-1/logs?tail=abc"))
}

type embeddedEvent struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}

type eventInCluster struct {
	ClusterID string `path:"clusterID" binding:"required"`
	embeddedEvent
}

// Corps embarqué à côté d'un paramètre de chemin (événements du watcher,
// mise à jour d'un conteneur) : avant, un champ nommé `json:",inline"`
// restait vide, encoding/json ignorant « inline ».
func TestBindHook_EmbeddedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tonic.SetBindHook(bindHook(tonic.DefaultMaxBodyBytes))
	defer tonic.SetBindHook(tonic.DefaultBindingHook)
	var got eventInCluster
	r := gin.New()
	r.POST("/clusters/:clusterID/events", tonic.Handler(func(_ *gin.Context, in *eventInCluster) error {
		got = *in
		return nil
	}, http.StatusNoContent))
	req := httptest.NewRequest("POST", "/clusters/k3d-dev/events", strings.NewReader(`{"name":"api-1","phase":"Failed"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, eventInCluster{ClusterID: "k3d-dev", embeddedEvent: embeddedEvent{Name: "api-1", Phase: "Failed"}}, got)
}

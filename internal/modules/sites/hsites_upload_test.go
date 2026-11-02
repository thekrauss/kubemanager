package sites

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
	"github.com/thekrauss/kubemanager/internal/modules/sites/service"
)

type fakeSites struct{ service.ISiteService }

func (fakeSites) MaxArchiveBytes() int64 { return 1 << 20 }
func (fakeSites) Deploy(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, io.Reader, int64) (*domain.SiteDeployment, error) {
	return &domain.SiteDeployment{}, nil
}

// Voir builds : c.Request remplacé, nettoyage à la charge du handler.
func TestUploadDeployment_RemovesTemporaryFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile(domain.ArchiveFormField, "a.zip")
	require.NoError(t, err)
	_, _ = part.Write(bytes.Repeat([]byte("x"), 4096))
	require.NoError(t, mw.Close())

	h := &SiteController{Service: fakeSites{}, Logger: zap.NewNop().Sugar()}
	e := gin.New()
	e.MaxMultipartMemory = 1
	e.Use(func(c *gin.Context) { c.Request = c.Request.WithContext(context.Background()) })
	e.POST("/", func(c *gin.Context) {
		h.UploadDeployment(c, &domain.UploadPath{ProjectID: uuid.NewString(), SiteID: uuid.NewString()})
	})
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	e.ServeHTTP(httptest.NewRecorder(), req)

	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries)
}

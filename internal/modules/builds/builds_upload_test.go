package builds

import (
	"bytes"
	"context"
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

	"github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	"github.com/thekrauss/kubemanager/internal/modules/builds/service"
)

// Le fichier temporaire de l'envoi disparaît, alors que les middlewares ont
// remplacé c.Request : net/http ne nettoie que la requête d'origine.
func TestUpload_RemovesTemporaryFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile(domain.ArchiveFormField, "a.tar.gz")
	require.NoError(t, err)
	_, _ = part.Write(bytes.Repeat([]byte("x"), 4096))
	require.NoError(t, mw.Close())

	h := &Controller{Service: &service.Service{Cfg: service.Config{MaxArchiveBytes: 1 << 20}}, Logger: zap.NewNop().Sugar()}
	e := gin.New()
	e.MaxMultipartMemory = 1
	e.Use(func(c *gin.Context) { c.Request = c.Request.WithContext(context.Background()) })
	e.POST("/", func(c *gin.Context) { h.Upload(c, &domain.UploadPath{ProjectID: uuid.NewString()}) })
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "archive refusée : pas un gzip")
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries)
}

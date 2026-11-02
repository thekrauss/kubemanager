package ginhttp

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Chaque morceau arrive au client sans attendre la fin du flux.
func TestCopyFlushing_DeliversBeforeEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pr, pw := io.Pipe()
	e := gin.New()
	e.GET("/", func(c *gin.Context) { CopyFlushing(c, pr) })
	srv := httptest.NewServer(e)
	defer srv.Close()
	defer pw.Close() // avant srv.Close : le handler lit encore le flux

	go func() { _, _ = pw.Write([]byte("ligne 1\n")) }()
	got := make(chan string, 1)
	go func() {
		resp, err := http.Get(srv.URL)
		if err != nil {
			got <- err.Error()
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 16)
		n, _ := resp.Body.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case line := <-got:
		assert.Equal(t, "ligne 1\n", line)
	case <-time.After(5 * time.Second):
		t.Fatal("rien reçu tant que le flux reste ouvert")
	}
}

// Formulaire analysé sur une copie de la requête : ses fichiers temporaires
// disparaissent quand même.
func TestRemoveMultipart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("archive", "a.zip")
	require.NoError(t, err)
	_, _ = part.Write(bytes.Repeat([]byte("x"), 4096))
	require.NoError(t, mw.Close())

	e := gin.New()
	e.MaxMultipartMemory = 1
	var during int
	e.POST("/", func(c *gin.Context) {
		defer RemoveMultipart(c)
		_, err := c.FormFile("archive")
		require.NoError(t, err)
		entries, _ := os.ReadDir(dir)
		during = len(entries)
	})
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	e.ServeHTTP(httptest.NewRecorder(), req)

	assert.Positive(t, during, "fichier temporaire pendant la requête")
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries)
}

func TestRemoveMultipart_NoForm(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	RemoveMultipart(c)
}

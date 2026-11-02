package workloads

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

type logsService struct {
	service.IWorkloadService
	got service.LogOptions
}

func (f *logsService) Logs(_ context.Context, _ uuid.UUID, opts service.LogOptions) (io.ReadCloser, error) {
	f.got = opts
	return io.NopCloser(strings.NewReader("ligne\n")), nil
}

// Sans tail ni since : la fenêtre par défaut, sans effacer follow et previous
// (avant : logs --follow s'arrêtait aussitôt).
func TestGetWorkloadLogs_KeepsFollow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &logsService{}
	h := NewWorkloadHandler(svc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	h.GetWorkloadLogs(c, &LogsRequest{ID: uuid.NewString(), Follow: true, Previous: true})

	def := service.DefaultLogOptions()
	assert.True(t, svc.got.Follow)
	assert.True(t, svc.got.Previous)
	assert.Equal(t, def.TailLines, svc.got.TailLines)
	assert.Equal(t, def.SinceSeconds, svc.got.SinceSeconds)
	assert.Equal(t, "ligne\n", w.Body.String())
	assert.True(t, w.Flushed, "le flux est transmis sans tampon")
}

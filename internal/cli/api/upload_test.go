package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Un envoi lent dépasse le délai des appels ordinaires sans être coupé (100 Mo à
// 1 Mbit/s prennent 13 min) ; un appel ordinaire reste borné.
func TestUpload_NotLimitedByCallTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	c := New(srv.URL)
	c.Source = staticSource("t")
	c.HTTP.Timeout = 50 * time.Millisecond

	require.NoError(t, c.Upload(context.Background(), "/up", "archive", "a.zip", strings.NewReader("x"), nil))
	assert.Error(t, c.Do(context.Background(), http.MethodGet, "/", nil, nil), "appel ordinaire toujours borné")
}

// Un envoi s'interrompt par le contexte (Ctrl-C), même sans délai global.
func TestUpload_CanceledByContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	c := New(srv.URL)
	c.Source = staticSource("t")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.Error(t, c.Upload(ctx, "/up", "archive", "a.zip", strings.NewReader("x"), nil))
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.True(t, errors.Is(ctx.Err(), context.DeadlineExceeded))
}

// Serveur muet : borné par le transport, y compris pour un envoi.
func TestNew_ResponseHeaderTimeout(t *testing.T) {
	c := New("http://x")
	tr, ok := c.HTTP.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, httpTimeout, tr.ResponseHeaderTimeout)
	assert.Equal(t, httpTimeout, c.HTTP.Timeout)
	assert.Zero(t, c.uploadClient().Timeout)
	assert.Same(t, c.HTTP.Transport, c.uploadClient().Transport)
}

package activities

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type zipEntry struct {
	name string
	body string
}

func buildZip(t *testing.T, entries ...zipEntry) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		require.NoError(t, err)
		_, err = w.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	return zr
}

type memPut struct {
	files map[string]string
	types map[string]string
}

func newMemPut() *memPut { return &memPut{files: map[string]string{}, types: map[string]string{}} }

func (m *memPut) put(_ context.Context, rel string, r io.Reader, _ int64, ct string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.files[rel] = string(b)
	m.types[rel] = ct
	return nil
}

func isArchiveErr(err error) bool {
	var ae *ArchiveError
	return errors.As(err, &ae)
}

func TestExtractArchive_HappyPath(t *testing.T) {
	zr := buildZip(t,
		zipEntry{"index.html", "<h1>hi</h1>"},
		zipEntry{"css/app.css", "body{}"},
		zipEntry{".well-known/security.txt", "x"},
	)
	m := newMemPut()
	res, err := ExtractArchive(context.Background(), zr, Limits{}, m.put)
	require.NoError(t, err)

	assert.Equal(t, 3, res.FileCount)
	assert.Equal(t, int64(len("<h1>hi</h1>")+len("body{}")+1), res.SizeBytes)
	assert.Equal(t, "<h1>hi</h1>", m.files["index.html"])
	assert.Equal(t, "text/html; charset=utf-8", m.types["index.html"])
	assert.Equal(t, "text/css; charset=utf-8", m.types["css/app.css"])
	assert.Contains(t, m.files, ".well-known/security.txt", "les dotfiles utiles sont gardés")
}

func TestExtractArchive_StripsSingleRootFolder(t *testing.T) {
	zr := buildZip(t, zipEntry{"dist/index.html", "a"}, zipEntry{"dist/js/app.js", "b"})
	m := newMemPut()
	_, err := ExtractArchive(context.Background(), zr, Limits{}, m.put)
	require.NoError(t, err)
	assert.Contains(t, m.files, "index.html")
	assert.Contains(t, m.files, "js/app.js")
}

func TestExtractArchive_IgnoresJunk(t *testing.T) {
	zr := buildZip(t,
		zipEntry{"index.html", "a"},
		zipEntry{"__MACOSX/._index.html", "junk"},
		zipEntry{".DS_Store", "junk"},
		zipEntry{".git/config", "secret"},
	)
	m := newMemPut()
	res, err := ExtractArchive(context.Background(), zr, Limits{}, m.put)
	require.NoError(t, err)
	assert.Equal(t, 1, res.FileCount)
}

func TestExtractArchive_RejectsZipSlip(t *testing.T) {
	for _, name := range []string{"../evil.html", "a/../../evil.html", "/etc/passwd", `..\evil.html`} {
		zr := buildZip(t, zipEntry{"index.html", "a"}, zipEntry{name, "x"})
		m := newMemPut()
		_, err := ExtractArchive(context.Background(), zr, Limits{}, m.put)
		assert.True(t, isArchiveErr(err), "doit refuser %q", name)
		assert.Empty(t, m.files, "rien ne doit être écrit pour %q", name)
	}
}

func TestExtractArchive_RequiresRootIndex(t *testing.T) {
	zr := buildZip(t, zipEntry{"about.html", "a"})
	_, err := ExtractArchive(context.Background(), zr, Limits{}, newMemPut().put)
	assert.True(t, isArchiveErr(err))
}

func TestExtractArchive_EnforcesLimitsBeforeWriting(t *testing.T) {
	zr := buildZip(t, zipEntry{"index.html", "aaaa"}, zipEntry{"b.txt", "bbbb"})

	m := newMemPut()
	_, err := ExtractArchive(context.Background(), zr, Limits{MaxFiles: 1}, m.put)
	assert.True(t, isArchiveErr(err))
	assert.Empty(t, m.files)

	m = newMemPut()
	_, err = ExtractArchive(context.Background(), zr, Limits{MaxExtractedBytes: 5}, m.put)
	assert.True(t, isArchiveErr(err))
	assert.Empty(t, m.files)
}

func TestExtractArchive_EmptyArchive(t *testing.T) {
	_, err := ExtractArchive(context.Background(), buildZip(t), Limits{}, newMemPut().put)
	assert.True(t, isArchiveErr(err))
}

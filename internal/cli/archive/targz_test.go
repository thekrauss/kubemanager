package archive

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

func listTar(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	names := []string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, h.Name)
		assert.Zero(t, h.Uid)
	}
	sort.Strings(names)
	return names
}

// Sans .dockerignore : ni dépendances, ni secrets locaux, ni historique Git.
func TestTarGz_DefaultExcludesSecrets(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"Dockerfile": "FROM x", "src/app.js": "1", ".env": "SECRET=1", ".env.prod": "S=2", "src/.env": "S=3", "tls.key": "k", "certs/a.pem": "p",
		"node_modules/a/b.js": "x", "web/node_modules/c.js": "x", ".git/config": "x",
	})
	res, err := TarGz(dir)
	require.NoError(t, err)
	defer os.Remove(res.Path)
	assert.Equal(t, []string{"Dockerfile", "src/app.js"}, listTar(t, res.Path))
	assert.Empty(t, res.Sensitive)
}

// Avec .dockerignore : mêmes règles que docker build, et avertissement si un
// fichier sensible part quand même.
func TestTarGz_DockerIgnore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"Dockerfile": "FROM x", ".dockerignore": "*.log\ndocs\n!docs/keep.md\n", "a.log": "x", "b.txt": "x",
		"docs/x.md": "x", "docs/keep.md": "x", ".env": "SECRET=1", ".git/HEAD": "x",
	})
	res, err := TarGz(dir)
	require.NoError(t, err)
	defer os.Remove(res.Path)
	assert.Equal(t, []string{".dockerignore", ".env", "Dockerfile", "b.txt", "docs/keep.md"}, listTar(t, res.Path))
	assert.Equal(t, []string{".env"}, res.Sensitive)
}

// Un lien symbolique reste un lien : son contenu n'est jamais lu.
func TestTarGz_SymlinkNotFollowed(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	write(t, dir, map[string]string{"Dockerfile": "FROM x"})
	write(t, outside, map[string]string{"secret": "TOP"})
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "lien")))
	res, err := TarGz(dir)
	require.NoError(t, err)
	defer os.Remove(res.Path)
	f, _ := os.Open(res.Path)
	defer f.Close()
	gz, _ := gzip.NewReader(f)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if h.Name == "lien" {
			assert.Equal(t, byte(tar.TypeSymlink), h.Typeflag)
			assert.Zero(t, h.Size)
		}
	}
}

func TestTarGz_Empty(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{".env": "x"})
	_, err := TarGz(dir)
	assert.ErrorIs(t, err, ErrEmpty)
}

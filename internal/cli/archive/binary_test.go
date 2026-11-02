package archive

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goBuild : vrai binaire pour la cible demandée.
func goBuild(t *testing.T, goos, goarch string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644))
	out := filepath.Join(dir, "app")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	return out
}

func TestBinaryTarGz(t *testing.T) {
	res, err := BinaryTarGz(goBuild(t, "linux", "amd64"))
	require.NoError(t, err)
	defer os.Remove(res.Path)
	assert.Equal(t, []string{"Dockerfile", "app"}, listTar(t, res.Path))

	_, err = BinaryTarGz(goBuild(t, "linux", "arm64"))
	assert.ErrorIs(t, err, ErrWrongArch)
	_, err = BinaryTarGz(goBuild(t, "darwin", "amd64"))
	assert.ErrorIs(t, err, ErrNotLinuxBinary)
	_, err = BinaryTarGz(goBuild(t, "windows", "amd64"))
	assert.ErrorIs(t, err, ErrNotLinuxBinary)

	text := filepath.Join(t.TempDir(), "script.sh")
	require.NoError(t, os.WriteFile(text, []byte("#!/bin/sh\n"), 0o755))
	_, err = BinaryTarGz(text)
	assert.ErrorIs(t, err, ErrNotLinuxBinary)

	_, err = BinaryTarGz(filepath.Join(t.TempDir(), "absent"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

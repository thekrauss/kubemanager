package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wI2L/fizz/openapi"

	"github.com/thekrauss/kubemanager/internal/core/i18n"
)

// Écrit la copie de la spec servie par la console quand l'API ne répond pas
// (kweb : lib/docs/snapshot). À relancer quand les routes changent :
//
//	OPENAPI_SNAPSHOT_DIR=../kweb/lib/docs/snapshot go test ./internal/infrastructure/router -run TestWriteOpenAPISnapshot
func TestWriteOpenAPISnapshot(t *testing.T) {
	dir := os.Getenv("OPENAPI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("OPENAPI_SNAPSHOT_DIR non défini")
	}
	if !filepath.IsAbs(dir) {
		// go test s'exécute dans le dossier du paquet.
		dir = filepath.Join("..", "..", "..", dir)
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))
	documentedAPI(t).Generator().SetInfo(&openapi.Info{Title: apiTitle, Description: apiDescription, Version: apiVersion})
	for name, locale := range map[string]i18n.Locale{"openapi.fr.json": i18n.FR, "openapi.en.json": i18n.EN} {
		raw, err := publicSpec(documentedAPI(t).Generator().API(), locale)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), raw, 0o644))
	}
}

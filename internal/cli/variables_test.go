package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// API minimale : un conteneur et ses variables.
func fakeVariablesAPI(t *testing.T, patched *map[string]any) string {
	t.Helper()
	id := uuid.New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /workloads", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"ID": id, "Name": "web", "Status": "RUNNING"}})
	})
	mux.HandleFunc("GET /workloads/{id}/variables", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"variables": []map[string]any{
			{"name": "LOG", "secret": false, "value": "info"}, {"name": "DB", "secret": true},
		}})
	})
	mux.HandleFunc("PATCH /workloads/{id}/variables", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(patched)
		_ = json.NewEncoder(w).Encode(map[string]any{"changed": false, "variables": []any{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_TOKEN", "ci-token")
	t.Setenv("BETORUN_API_URL", srv.URL)
	t.Setenv("BETORUN_PROJECT", projectID)
	return srv.URL
}

func TestSecrets_NeverOnCommandLine(t *testing.T) {
	var patched map[string]any
	fakeVariablesAPI(t, &patched)
	_, err := run(t, "workloads", "secrets", "set", "web", "DB=motdepasse")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "historique du shell")
	assert.NotContains(t, err.Error(), "motdepasse", "la valeur n'est jamais répétée")
	assert.Nil(t, patched)
}

func TestSecrets_PromptAndFromEnv(t *testing.T) {
	var patched map[string]any
	fakeVariablesAPI(t, &patched)

	saved := readCode
	defer func() { readCode = saved }()
	var asked []string
	readCode = func(prompt string) (string, error) { asked = append(asked, prompt); return "tapé", nil }
	_, err := run(t, "workloads", "secrets", "set", "web", "DB")
	require.NoError(t, err)
	assert.Equal(t, []string{"Valeur de DB :"}, asked)
	assert.Equal(t, map[string]any{"DB": "tapé"}, patched["secrets"])

	t.Setenv("MON_SECRET", "depuis-env")
	_, err = run(t, "workloads", "secrets", "set", "web", "MON_SECRET", "--from-env")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"MON_SECRET": "depuis-env"}, patched["secrets"])

	_, err = run(t, "workloads", "secrets", "set", "web", "ABSENTE_XYZ", "--from-env")
	require.Error(t, err)
}

func TestSecrets_ListHidesValue(t *testing.T) {
	var patched map[string]any
	fakeVariablesAPI(t, &patched)
	out, err := run(t, "workloads", "env", "list", "web")
	require.NoError(t, err)
	assert.Contains(t, out, "info")
	assert.Contains(t, out, "••••••")
}

func TestEnv_SetAndBadArgument(t *testing.T) {
	var patched map[string]any
	fakeVariablesAPI(t, &patched)
	_, err := run(t, "workloads", "env", "set", "web", "A=1", "B=c=d")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"A": "1", "B": "c=d"}, patched["env"])
	_, err = run(t, "workloads", "env", "set", "web", "SANSEGAL")
	require.Error(t, err)
}

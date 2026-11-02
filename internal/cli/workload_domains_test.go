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

func TestWorkloadDomains(t *testing.T) {
	wid, did := uuid.New(), uuid.New()
	verified, pointing := false, false
	var added map[string]any
	view := func() map[string]any {
		return map[string]any{"id": did, "hostname": "www.exemple.sn", "verified": verified, "pointing": pointing, "records": []map[string]string{
			{"type": "TXT", "name": "_betorun-challenge.www.exemple.sn", "value": "betorun-verify=abc"},
			{"type": "A", "name": "www.exemple.sn", "value": "203.0.113.10"},
		}}
	}
	removed := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /workloads", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"ID": wid, "Name": "web"}})
	})
	mux.HandleFunc("POST /workloads/{id}/domains", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&added)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(view())
	})
	mux.HandleFunc("GET /workloads/{id}/domains", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{view()})
	})
	mux.HandleFunc("POST /workloads/{id}/domains/{d}/verify", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(view())
	})
	mux.HandleFunc("DELETE /workloads/{id}/domains/{d}", func(w http.ResponseWriter, r *http.Request) {
		removed = r.PathValue("d") == did.String()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_TOKEN", "ci-token")
	t.Setenv("BETORUN_API_URL", srv.URL)
	t.Setenv("BETORUN_PROJECT", projectID)

	out, err := run(t, "workloads", "domains", "add", "web", "www.exemple.sn")
	require.NoError(t, err)
	assert.Equal(t, "www.exemple.sn", added["hostname"])
	assert.Contains(t, out, "_betorun-challenge.www.exemple.sn")
	assert.Contains(t, out, "betorun-verify=abc")
	assert.Contains(t, out, "betorun workloads domains verify web www.exemple.sn")

	out, err = run(t, "workloads", "domains", "verify", "web", "www.exemple.sn")
	require.NoError(t, err)
	assert.Contains(t, out, "Preuve introuvable")
	assert.Contains(t, out, "betorun-verify=abc", "les enregistrements sont rappelés")

	verified = true
	out, err = run(t, "workloads", "domains", "verify", "web", "WWW.exemple.sn")
	require.NoError(t, err)
	assert.Contains(t, out, "vérifié")
	assert.Contains(t, out, "ne pointe pas encore", "certificat impossible tant que le domaine ne pointe pas ici")

	out, err = run(t, "workloads", "domains", "list", "web")
	require.NoError(t, err)
	assert.Contains(t, out, "vérifié")

	_, err = run(t, "workloads", "domains", "verify", "web", "autre.sn")
	require.Error(t, err)

	_, err = run(t, "workloads", "domains", "remove", "web", "www.exemple.sn")
	require.NoError(t, err)
	assert.True(t, removed)
}

func TestWorkloadGit(t *testing.T) {
	wid, bid := uuid.New(), uuid.New()
	sha := "0123456789abcdef0123456789abcdef01234567"
	link := map[string]any{"repo_full_name": "awa/shop", "branch": "main", "auto_deploy": true, "disabled_reason": ""}
	deployed := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /workloads", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"ID": wid, "Name": "web"}})
	})
	mux.HandleFunc("GET /workloads/{id}/github", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "linked": true, "link": link, "deployed_commit": sha})
	})
	mux.HandleFunc("POST /workloads/{id}/github/deploy", func(w http.ResponseWriter, _ *http.Request) {
		deployed = true
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": bid, "status": "QUEUED", "git_commit": sha})
	})
	mux.HandleFunc("GET /projects/{p}/builds/{b}", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": bid, "status": "SUCCEEDED", "git_commit": sha})
	})
	mux.HandleFunc("GET /projects/{p}/builds/{b}/logs", func(w http.ResponseWriter, _ *http.Request) {})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_TOKEN", "ci-token")
	t.Setenv("BETORUN_API_URL", srv.URL)
	t.Setenv("BETORUN_PROJECT", projectID)

	out, err := run(t, "workloads", "git", "status", "web")
	require.NoError(t, err)
	assert.Contains(t, out, "awa/shop")
	assert.Contains(t, out, "0123456")

	out, err = run(t, "workloads", "git", "deploy", "web")
	require.NoError(t, err)
	assert.True(t, deployed)
	assert.Contains(t, out, "0123456")

	link["disabled_reason"] = "INSTALLATION_DELETED"
	out, err = run(t, "workloads", "git", "status", "web")
	require.NoError(t, err)
	assert.Contains(t, out, "désinstallée")
}

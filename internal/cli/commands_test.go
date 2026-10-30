package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOps : routes conteneurs, domaines et accès (jeton de CI fourni).
type fakeOps struct {
	mu         sync.Mutex
	workloads  []map[string]any
	created    map[string]any
	statusGets atomic.Int32
	logsQuery  string
	actions    []string
	deleted    bool
	domains    []map[string]any
	policy     map[string]any
	etagFails  atomic.Int32 // nombre de 409 à renvoyer avant d'accepter
	setCalls   atomic.Int32
}

func newFakeOps(t *testing.T) (*httptest.Server, *fakeOps) {
	f := &fakeOps{policy: map[string]any{"version": 1, "etag": "e1", "bindings": []any{
		map[string]any{"role": "roles/owner", "members": []any{"user:11111111-1111-1111-1111-111111111111"}},
	}}}
	wlID := uuid.NewString()
	siteID := uuid.NewString()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ci-token" {
			write(w, 401, map[string]string{"error": "invalid token"})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		p := strings.TrimPrefix(r.URL.Path, "/kmanager/v1")
		switch {
		// ── conteneurs ──
		case r.Method == "GET" && p == "/workloads":
			assert.Equal(t, projectID, r.URL.Query().Get("project_id"))
			write(w, 200, f.workloads)
		case r.Method == "POST" && p == "/workloads":
			_ = json.NewDecoder(r.Body).Decode(&f.created)
			f.workloads = append(f.workloads, map[string]any{"ID": wlID, "Name": f.created["name"],
				"Image": f.created["image"], "Status": "STARTING", "CurrentPhase": "HELM_CHART_LOADING", "Replicas": 2})
			write(w, 202, map[string]any{"workload_id": wlID, "status": "STARTING"})
		case r.Method == "GET" && p == "/workloads/"+wlID:
			wl := f.workloads[0]
			if f.statusGets.Add(1) >= 2 {
				wl["Status"], wl["CurrentPhase"], wl["ExternalURL"] = "RUNNING", "READY", "https://api.betorun.app"
			}
			write(w, 200, wl)
		case r.Method == "GET" && p == "/workloads/"+wlID+"/logs":
			f.logsQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("listening on :8080\nGET /health 200\n"))
		case r.Method == "POST" && strings.HasPrefix(p, "/workloads/"+wlID+"/"):
			f.actions = append(f.actions, strings.TrimPrefix(p, "/workloads/"+wlID+"/"))
			write(w, 200, map[string]string{})
		case r.Method == "DELETE" && p == "/workloads/"+wlID:
			f.deleted = true
			write(w, 202, map[string]string{})
		// ── sites et domaines ──
		case r.Method == "GET" && p == "/projects/"+projectID+"/sites":
			write(w, 200, []any{map[string]any{"id": siteID, "name": "vitrine", "status": "LIVE", "url": "https://vitrine.betorun.app"}})
		case r.Method == "GET" && p == "/projects/"+projectID+"/sites/"+siteID+"/domains":
			write(w, 200, f.domains)
		case r.Method == "POST" && p == "/projects/"+projectID+"/sites/"+siteID+"/domains":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			d := map[string]any{"id": uuid.NewString(), "hostname": body["hostname"], "verified": false, "cname_target": "edge.betorun.app"}
			f.domains = append(f.domains, d)
			write(w, 201, d)
		case r.Method == "POST" && strings.HasSuffix(p, "/verify"):
			f.domains[0]["verified"] = true
			write(w, 200, f.domains[0])
		case r.Method == "DELETE" && strings.Contains(p, "/domains/"):
			f.domains = nil
			w.WriteHeader(204)
		// ── accès ──
		case r.Method == "GET" && p == "/iam/policy":
			assert.Equal(t, "projects/"+projectID, r.URL.Query().Get("resource"))
			write(w, 200, map[string]any{"resource": "projects/" + projectID, "policy": f.policy, "members": []any{
				map[string]any{"principal": "user:11111111-1111-1111-1111-111111111111", "email": "awa@ex.sn"},
				map[string]any{"principal": "user:22222222-2222-2222-2222-222222222222", "email": "moussa@ex.sn"},
			}})
		case r.Method == "POST" && p == "/iam/policy":
			f.setCalls.Add(1)
			var body struct {
				Policy map[string]any `json:"policy"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if f.etagFails.Load() > 0 {
				f.etagFails.Add(-1)
				write(w, 409, map[string]string{"code": "ABORTED", "message": "the policy was modified concurrently"})
				return
			}
			assert.Equal(t, f.policy["etag"], body.Policy["etag"], "etag de la lecture renvoyé")
			body.Policy["etag"] = "e" + uuid.NewString()[:4]
			f.policy = body.Policy
			write(w, 200, map[string]any{"policy": f.policy})
		case r.Method == "GET" && p == "/iam/roles":
			write(w, 200, []any{map[string]any{"name": "roles/viewer", "description": "Lecture"}})
		default:
			t.Errorf("route inattendue %s %s", r.Method, p)
			w.WriteHeader(404)
		}
	})
	return srv, f
}

func TestCLI_WorkloadsDomainsAccess(t *testing.T) {
	srv, f := newFakeOps(t)
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_API_URL", srv.URL+"/kmanager/v1")
	t.Setenv("BETORUN_PROJECT", projectID)
	t.Setenv("BETORUN_TOKEN", "ci-token")
	workloadPollEvery = 5 * time.Millisecond

	// ── conteneurs ──
	out, err := run(t, "workloads", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "Aucun conteneur")
	_, err = run(t, "workloads", "deploy", "--image", "nginx:1")
	assert.Contains(t, describe(err), "--name")
	_, err = run(t, "workloads", "deploy", "-n", "api", "-i", "nginx:1", "-e", "PASDEGAL")
	assert.Contains(t, describe(err), "CLE=valeur")

	out, err = run(t, "wl", "deploy", "-n", "api", "-i", "ghcr.io/sunupay/api:1.2", "--port", "3000", "--replicas", "2",
		"-e", "NODE_ENV=production", "-e", "URL=postgres://x?a=b", "--service-account", uuid.Nil.String())
	require.NoError(t, err, out)
	assert.Contains(t, out, "En service : https://api.betorun.app")
	assert.Equal(t, projectID, f.created["project_id"])
	assert.Equal(t, float64(3000), f.created["target_port"])
	assert.Equal(t, map[string]any{"NODE_ENV": "production", "URL": "postgres://x?a=b"}, f.created["env_vars"], "« = » dans la valeur conservé")
	assert.Equal(t, "serviceAccount:"+uuid.Nil.String(), f.created["service_account"])

	out, err = run(t, "workloads", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "api")
	assert.Contains(t, out, "RUNNING")

	out, err = run(t, "workloads", "logs", "api", "-f", "--tail", "50", "--since", "10m")
	require.NoError(t, err)
	assert.Contains(t, out, "GET /health 200")
	assert.Contains(t, f.logsQuery, "follow=true")
	assert.Contains(t, f.logsQuery, "tail=50")
	assert.Contains(t, f.logsQuery, "since=600")

	for _, a := range []string{"restart", "stop", "start"} {
		_, err = run(t, "workloads", a, "api")
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"restart", "stop", "start"}, f.actions)
	_, err = run(t, "workloads", "restart", "inconnu")
	assert.Contains(t, describe(err), `aucun conteneur nommé "inconnu"`)

	out, err = run(t, "workloads", "status", "api")
	require.NoError(t, err)
	assert.Contains(t, out, "RUNNING (READY)")

	// Suppression : confirmation exigée (nom retapé), sauf --yes.
	root := newRoot("betorun")
	root.SetIn(strings.NewReader("pas-le-bon\n"))
	root.SetOut(&strings.Builder{})
	root.SetArgs([]string{"workloads", "delete", "api"})
	assert.Contains(t, describe(root.Execute()), "annulée")
	assert.False(t, f.deleted)
	root = newRoot("betorun")
	root.SetIn(strings.NewReader("api\n"))
	root.SetOut(&strings.Builder{})
	root.SetArgs([]string{"workloads", "delete", "api"})
	require.NoError(t, root.Execute())
	assert.True(t, f.deleted)

	// ── domaines ──
	_, err = run(t, "sites", "domains", "list")
	assert.Contains(t, describe(err), "--site")
	out, err = run(t, "sites", "domains", "add", "www.sunupay.sn", "--site", "vitrine")
	require.NoError(t, err)
	assert.Contains(t, out, "www.sunupay.sn  CNAME  edge.betorun.app")
	assert.Contains(t, out, "betorun sites domains verify --site vitrine www.sunupay.sn")
	out, err = run(t, "sites", "domains", "list", "--site", "vitrine")
	require.NoError(t, err)
	assert.Contains(t, out, "à vérifier")
	out, err = run(t, "sites", "domains", "verify", "WWW.SUNUPAY.SN", "--site", "vitrine")
	require.NoError(t, err)
	assert.Contains(t, out, "est vérifié")
	_, err = run(t, "sites", "domains", "remove", "autre.sn", "--site", "vitrine")
	assert.Contains(t, describe(err), "n'est pas rattaché")
	_, err = run(t, "sites", "domains", "remove", "www.sunupay.sn", "--site", "vitrine")
	require.NoError(t, err)

	// ── accès ──
	_, err = run(t, "access", "grant", "moussa@ex.sn", "developer", "--until", "31/12/2026")
	assert.Contains(t, describe(err), "AAAA-MM-JJ")
	f.etagFails.Store(1) // écriture concurrente : relu puis réécrit
	out, err = run(t, "access", "grant", "moussa@ex.sn", "developer", "--until", "2026-12-31")
	require.NoError(t, err, out)
	assert.Equal(t, int32(2), f.setCalls.Load(), "un 409 puis une réussite")
	bindings := f.policy["bindings"].([]any)
	last := bindings[len(bindings)-1].(map[string]any)
	assert.Equal(t, "roles/developer", last["role"])
	assert.Equal(t, []any{"user:22222222-2222-2222-2222-222222222222"}, last["members"], "email résolu en principal existant")
	assert.Contains(t, last["condition"].(map[string]any)["expression"], "2026-12-31T23:59:59Z")

	out, err = run(t, "access", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "moussa@ex.sn")
	assert.Contains(t, out, "Expire le 2026-12-31")

	_, err = run(t, "access", "revoke", "inconnu@ex.sn")
	assert.Contains(t, describe(err), "n'a pas ce rôle")
	out, err = run(t, "access", "revoke", "moussa@ex.sn")
	require.NoError(t, err)
	assert.Contains(t, out, "(1)")
	for _, b := range f.policy["bindings"].([]any) {
		assert.NotEqual(t, "roles/developer", b.(map[string]any)["role"], "binding vide retiré")
	}

	out, err = run(t, "access", "roles")
	require.NoError(t, err)
	assert.Contains(t, out, "viewer")
}

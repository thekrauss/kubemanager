package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	iamOrg   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	iamGroup = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	iamSA    = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

// fakeIAM : API IAM en mémoire (groupes, rôles personnalisés, comptes de
// service, clés, relations de confiance, policy du projet).
type fakeIAM struct {
	mu       sync.Mutex
	members  []string
	roles    map[string]map[string]any // nom complet → rôle
	sas      []map[string]any
	trusts   []map[string]any
	bindings []map[string]any
	etag     int
}

func newFakeIAM(t *testing.T) (*httptest.Server, *fakeIAM) {
	f := &fakeIAM{roles: map[string]map[string]any{}}
	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := strings.TrimPrefix(r.URL.Path, "/kmanager/v1")
		groups := "/organizations/" + iamOrg + "/groups"
		sas := "/projects/" + projectID + "/serviceAccounts"
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		if p != "/iam/.well-known/openid-configuration" && r.Header.Get("Authorization") != "Bearer ci-token" {
			write(w, 401, map[string]string{"error": "invalid token"})
			return
		}
		switch {
		case p == "/iam/.well-known/openid-configuration":
			write(w, 200, map[string]string{"issuer": "https://api.betorun.app/kmanager/v1/iam"})
		case p == "/iam/ancestry":
			write(w, 200, map[string]any{"ancestors": []string{"projects/" + projectID, "organizations/" + iamOrg, "platform"}})
		case p == "/iam/roles":
			write(w, 200, []any{map[string]any{"name": "roles/viewer", "description": "Lecture"}})
		case p == "/iam/permissions":
			write(w, 200, []any{
				map[string]any{"name": "workloads.workloads.list", "title": "Lister", "custom_roles": true},
				map[string]any{"name": "workloads.workloads.update", "title": "Mettre à jour", "custom_roles": true},
				map[string]any{"name": "resourcemanager.projects.setIamPolicy", "title": "Accès", "custom_roles": false},
			})
		// ── groupes ──
		case r.Method == "GET" && p == groups:
			write(w, 200, []any{map[string]any{"id": iamGroup, "group_id": "equipe-paiement", "display_name": "Équipe paiement",
				"principal": "group:" + iamGroup, "member_count": len(f.members)}})
		case r.Method == "POST" && p == groups:
			write(w, 201, map[string]any{"id": iamGroup, "group_id": body["group_id"], "principal": "group:" + iamGroup})
		case r.Method == "POST" && p == groups+"/"+iamGroup+"/members":
			f.members = append(f.members, body["member"].(string))
			write(w, 201, map[string]any{"principal": body["member"]})
		case r.Method == "GET" && p == groups+"/"+iamGroup+"/members":
			var out []any
			for _, m := range f.members {
				out = append(out, map[string]any{"principal": m})
			}
			write(w, 200, out)
		case r.Method == "DELETE" && p == groups+"/"+iamGroup+"/members":
			f.members = nil
			w.WriteHeader(204)
		// ── rôles personnalisés ──
		case r.Method == "GET" && p == "/iam/customRoles":
			var out []any
			for _, role := range f.roles {
				if role["parent"] == r.URL.Query().Get("parent") {
					out = append(out, role)
				}
			}
			write(w, 200, out)
		case r.Method == "POST" && p == "/iam/customRoles":
			name := body["parent"].(string) + "/roles/" + body["role_id"].(string)
			body["name"], body["etag"] = name, "r1"
			f.roles[name] = body
			write(w, 201, body)
		case r.Method == "PATCH" && p == "/iam/customRole":
			role := f.roles[body["name"].(string)]
			assert.Equal(t, role["etag"], body["etag"], "etag de la lecture")
			role["permissions"], role["etag"] = body["permissions"], "r2"
			write(w, 200, role)
		case r.Method == "DELETE" && p == "/iam/customRole":
			delete(f.roles, r.URL.Query().Get("name"))
			w.WriteHeader(204)
		// ── comptes de service ──
		case r.Method == "GET" && p == sas:
			write(w, 200, f.sas)
		case r.Method == "POST" && p == sas:
			sa := map[string]any{"id": iamSA, "account_id": body["account_id"], "principal": "serviceAccount:" + iamSA}
			f.sas = append(f.sas, sa)
			write(w, 201, sa)
		case r.Method == "POST" && p == sas+"/"+iamSA+"/keys":
			write(w, 201, map[string]any{"key_id": "k1", "valid_before": "2027-01-28T00:00:00Z",
				"key_file": map[string]any{"type": "betorun_service_account_key", "project_id": projectID, "key_id": "k1",
					"principal": "serviceAccount:" + iamSA, "private_key": "PEM", "token_uri": "https://api/iam/token"}})
		case r.Method == "POST" && p == sas+"/"+iamSA+"/federation":
			f.trusts = append(f.trusts, body)
			body["id"] = "t1"
			write(w, 201, body)
		// ── policy du projet ──
		case r.Method == "GET" && p == "/iam/policy":
			write(w, 200, map[string]any{"policy": map[string]any{"etag": fmt.Sprint(f.etag), "bindings": f.bindings}})
		case r.Method == "POST" && p == "/iam/policy":
			pol := body["policy"].(map[string]any)
			f.bindings = nil
			for _, b := range pol["bindings"].([]any) {
				f.bindings = append(f.bindings, b.(map[string]any))
			}
			f.etag++
			write(w, 200, map[string]any{"policy": pol})
		default:
			t.Errorf("route inattendue %s %s", r.Method, p)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, f
}

func TestCLI_IAM(t *testing.T) {
	srv, f := newFakeIAM(t)
	dir := t.TempDir()
	t.Setenv("BETORUN_CONFIG_DIR", dir)
	t.Setenv("BETORUN_LANG", "fr")
	t.Setenv("BETORUN_API_URL", srv.URL+"/kmanager/v1")
	t.Setenv("BETORUN_PROJECT", projectID)
	t.Setenv("BETORUN_TOKEN", "ci-token")

	// ── Groupes : désignés par leur nom ──
	out, err := run(t, "iam", "groups", "create", "equipe-paiement")
	require.NoError(t, err, out)
	assert.Contains(t, out, "Groupe equipe-paiement créé")
	_, err = run(t, "iam", "groups", "add-member", "equipe-paiement", "moussa@ex.sn")
	require.NoError(t, err)
	assert.Equal(t, []string{"user:moussa@ex.sn"}, f.members)
	out, err = run(t, "iam", "groups", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "1 membre(s)")
	_, err = run(t, "iam", "groups", "add-member", "inconnu", "moussa@ex.sn")
	assert.Contains(t, describe(err), `aucun groupe "inconnu"`)
	_, err = run(t, "iam", "groups", "remove-member", "equipe-paiement", "moussa@ex.sn")
	require.NoError(t, err)
	assert.Empty(t, f.members)

	// ── Rôles personnalisés ──
	_, err = run(t, "iam", "roles", "create", "ciDeployer")
	assert.Contains(t, describe(err), "--permission")
	out, err = run(t, "iam", "roles", "create", "ciDeployer", "--title", "Déploiement CI",
		"--permission", "workloads.workloads.list,workloads.workloads.update")
	require.NoError(t, err, out)
	assert.Contains(t, out, "2 permission(s)")
	_, err = run(t, "iam", "roles", "update", "ciDeployer", "--remove-permission", "workloads.workloads.list")
	require.NoError(t, err)
	assert.Equal(t, []any{"workloads.workloads.update"}, f.roles["projects/"+projectID+"/roles/ciDeployer"]["permissions"])
	out, err = run(t, "iam", "roles")
	require.NoError(t, err)
	assert.Contains(t, out, "viewer")
	assert.Contains(t, out, "ciDeployer")
	out, err = run(t, "iam", "permissions")
	require.NoError(t, err)
	assert.Contains(t, out, "workloads.workloads.update")
	assert.NotContains(t, out, "setIamPolicy", "réservée aux rôles prédéfinis")

	// ── Comptes de service, clé, CI sans secret ──
	out, err = run(t, "iam", "sa", "create", "ci-deployer")
	require.NoError(t, err, out)
	assert.Contains(t, out, "serviceAccount:"+iamSA)

	keyPath := filepath.Join(dir, "ci.json")
	out, err = run(t, "iam", "sa", "keys", "create", "ci-deployer", "--output", keyPath)
	require.NoError(t, err, out)
	info, err := os.Stat(keyPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "fichier lisible par son seul propriétaire")
	_, err = run(t, "iam", "sa", "keys", "create", "ci-deployer", "--output", keyPath)
	assert.Contains(t, describe(err), "existe déjà", "jamais écrasé")

	out, err = run(t, "iam", "sa", "trust", "github", "ci-deployer", "--repo", "sunupay/api", "--environment", "production")
	require.NoError(t, err, out)
	assert.Equal(t, "repo:sunupay/api:environment:production", f.trusts[0]["subject_pattern"])
	assert.Equal(t, issuerGitHub, f.trusts[0]["issuer"])
	assert.Contains(t, out, "id-token: write")
	assert.Contains(t, out, "    environment: production", "le job déclare l'environnement autorisé")
	assert.Contains(t, out, "BETORUN_SERVICE_ACCOUNT: serviceAccount:"+iamSA)
	out, err = run(t, "iam", "sa", "trust", "gitlab", "ci-deployer", "--project", "sunupay/api")
	require.NoError(t, err, out)
	assert.Equal(t, "project_path:sunupay/api:ref_type:branch:ref:main", f.trusts[1]["subject_pattern"])
	assert.Contains(t, out, "aud: https://api.betorun.app/kmanager/v1/iam", "audience = émetteur du STS")
	assert.Contains(t, out, `$CI_COMMIT_BRANCH == "main"`)
	_, err = run(t, "iam", "sa", "trust", "github", "ci-deployer", "--repo", "sunupay/api", "--branch", "main", "--tag", "v*")
	assert.Contains(t, describe(err), "une seule cible")

	// ── Accès : groupe, compte et rôle personnalisé désignés par leur nom ──
	_, err = run(t, "iam", "grant", "group:equipe-paiement", "viewer")
	require.NoError(t, err)
	_, err = run(t, "iam", "grant", "serviceAccount:ci-deployer", "ciDeployer")
	require.NoError(t, err)
	require.Len(t, f.bindings, 2)
	assert.Equal(t, "roles/viewer", f.bindings[0]["role"])
	assert.Equal(t, []any{"group:" + iamGroup}, f.bindings[0]["members"])
	assert.Equal(t, "projects/"+projectID+"/roles/ciDeployer", f.bindings[1]["role"])
	assert.Equal(t, []any{"serviceAccount:" + iamSA}, f.bindings[1]["members"])
	_, err = run(t, "iam", "revoke", "sa:ci-deployer")
	require.NoError(t, err)
	assert.Len(t, f.bindings, 1)
}

func TestGitHubAndGitLabSubjects(t *testing.T) {
	s, err := githubSubject("sunupay/api", "", "", "", false)
	require.NoError(t, err)
	assert.Equal(t, "repo:sunupay/api:ref:refs/heads/main", s, "branche main par défaut")
	s, _ = githubSubject("sunupay/api", "", "v*", "", false)
	assert.Equal(t, "repo:sunupay/api:ref:refs/tags/v*", s)
	s, _ = githubSubject("sunupay/api", "", "", "", true)
	assert.Equal(t, "repo:sunupay/api:pull_request", s)
	_, err = githubSubject("api", "", "", "", false)
	assert.Error(t, err, "propriétaire obligatoire")
	_, err = githubSubject("sunupay/*", "", "", "", false)
	assert.Error(t, err)

	s, _ = gitlabSubject("sunupay/api", "", "v1.0")
	assert.Equal(t, "project_path:sunupay/api:ref_type:tag:ref:v1.0", s)
}

package activities

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

// fakeAPIServer : juste assez de l'API Kubernetes pour que Helm conclue
// « aucune release » ; compte les appels reçus.
func fakeAPIServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/version" {
			fmt.Fprint(w, `{"major":"1","minor":"30","gitVersion":"v1.30.0"}`)
			return
		}
		fmt.Fprint(w, `{"kind":"SecretList","apiVersion":"v1","items":[]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// Helm vise le cluster de la configuration de la plateforme, jamais celui de
// KUBECONFIG ou de ~/.kube/config : sur un serveur séparé du cluster, il
// échouait ou visait un autre cluster.
func TestHelmUsesConfiguredCluster(t *testing.T) {
	configured, configuredCalls := fakeAPIServer(t)
	other, otherCalls := fakeAPIServer(t)

	kubeconfig := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(kubeconfig, []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: autre
  cluster: {server: %q}
contexts:
- name: autre
  context: {cluster: autre, user: u}
current-context: autre
users:
- name: u
  user: {}
`, other.URL)), 0o600))
	t.Setenv("KUBECONFIG", kubeconfig)

	a := &WorkloadActivities{K8sConfig: &rest.Config{Host: configured.URL}}
	require.NoError(t, a.UninstallChart(context.Background(), "km-x", "web"))

	assert.Positive(t, configuredCalls.Load(), "appels vers le cluster configuré")
	assert.Zero(t, otherCalls.Load(), "aucun appel vers le cluster de KUBECONFIG")
}

// Package e2e : parcours de bout en bout contre une pile en marche (API, worker,
// Temporal, Postgres, cluster). Ignoré sans E2E=1 ; le mode d'emploi et les
// variables sont dans README.txt.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const apiPrefix = "/kmanager/v1"

// Image sans root qui écoute sur 8080 : compatible avec le chart par défaut.
const (
	defaultImage = "nginxinc/nginx-unprivileged:1.27-alpine"
	imagePort    = 8080
	imageUID     = 101
	// closedPort : rien n'y écoute. Les sondes échouent, le déploiement reste en
	// attente : c'est la fenêtre où éprouver suppression et pannes.
	closedPort = 9099
)

// Délais d'attente, au-dessus de ce que le code autorise.
const (
	pollEvery      = 3 * time.Second
	projectReady   = 3 * time.Minute
	workloadReady  = 5 * time.Minute
	workloadGone   = 3 * time.Minute
	startingBefore = 2 * time.Minute
	// notReadyWithin : une seule attente de Helm (5 min) et l'écriture de l'état.
	// Avant la correction : environ 15 min.
	notReadyWithin = 8 * time.Minute
	// deleteAnswersWithin : l'attente de l'arrêt du déploiement (30 s) et une marge.
	deleteAnswersWithin = 45 * time.Second
	buildDone           = 15 * time.Minute
)

type env struct {
	t      *testing.T
	url    string
	token  string
	image  string
	kube   kubernetes.Interface
	client *http.Client
}

func setup(t *testing.T) *env {
	t.Helper()
	if os.Getenv("E2E") == "" {
		t.Skip("E2E not set")
	}
	e := &env{
		t:      t,
		url:    strings.TrimRight(getenv("E2E_URL", "http://localhost:8080"), "/"),
		image:  getenv("E2E_IMAGE", defaultImage),
		client: &http.Client{Timeout: 60 * time.Second},
	}
	e.token = os.Getenv("E2E_TOKEN")
	if e.token == "" {
		e.token = e.login()
	}
	if path := os.Getenv("E2E_KUBECONFIG"); path != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", path)
		require.NoError(t, err)
		e.kube, err = kubernetes.NewForConfig(cfg)
		require.NoError(t, err)
	}
	return e
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func (e *env) login() string {
	e.t.Helper()
	email, password := os.Getenv("E2E_EMAIL"), os.Getenv("E2E_PASSWORD")
	require.NotEmpty(e.t, email, "E2E_TOKEN, ou E2E_EMAIL et E2E_PASSWORD")
	var out struct {
		Status      string `json:"status"`
		AccessToken string `json:"access_token"`
	}
	code, raw := e.call(http.MethodPost, "/identity/login", map[string]string{"email": email, "password": password}, &out)
	require.Equal(e.t, http.StatusOK, code, raw)
	require.Equal(e.t, "authenticated", out.Status, "compte de test sans second facteur")
	return out.AccessToken
}

// call : requête JSON ; renvoie le code et le corps brut (pour les messages d'échec).
func (e *env) call(method, path string, body, out any) (int, string) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(e.t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.url+apiPrefix+path, reader)
	require.NoError(e.t, err)
	req.Header.Set("Content-Type", "application/json")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := e.client.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(e.t, err)
	if out != nil && resp.StatusCode < 300 && len(raw) > 0 {
		require.NoError(e.t, json.Unmarshal(raw, out), string(raw))
	}
	return resp.StatusCode, string(raw)
}

func (e *env) newProject() string {
	e.t.Helper()
	var out struct {
		ProjectID string `json:"project_id"`
	}
	code, raw := e.call(http.MethodPost, "/projects", map[string]string{
		"name": "e2e-" + uuid.NewString()[:8], "description": "test de bout en bout",
		"cpu_limit": "2000m", "memory_limit": "2048Mi",
	}, &out)
	require.Less(e.t, code, 300, raw)
	require.NotEmpty(e.t, out.ProjectID, raw)
	e.t.Cleanup(func() { e.call(http.MethodDelete, "/projects/"+out.ProjectID, nil, nil) })
	e.waitFor(projectReady, "projet READY", func() (bool, error) {
		var st struct {
			Status string `json:"status"`
		}
		code, raw := e.call(http.MethodGet, "/projects/"+out.ProjectID+"/status", nil, &st)
		if code != http.StatusOK {
			return false, fmt.Errorf("statut du projet : %d %s", code, raw)
		}
		if st.Status == "FAILED" {
			return false, fmt.Errorf("projet en échec : %s", raw)
		}
		return st.Status == "READY", nil
	})
	return out.ProjectID
}

type workload struct {
	ID, Name, Project string
}

func (e *env) deploy(project, image string, port int) workload {
	e.t.Helper()
	name := "w" + uuid.NewString()[:8]
	var out struct {
		WorkloadID string `json:"workload_id"`
	}
	code, raw := e.call(http.MethodPost, "/workloads", map[string]any{
		"project_id": project, "name": name, "image": image, "replicas": 1, "target_port": port,
		"cpu_limit": "200m", "memory_limit": "256Mi", "run_as_user": imageUID, "run_as_group": imageUID,
	}, &out)
	require.Equal(e.t, http.StatusAccepted, code, raw)
	require.NotEmpty(e.t, out.WorkloadID, raw)
	w := workload{ID: out.WorkloadID, Name: name, Project: project}
	e.t.Cleanup(func() { e.call(http.MethodDelete, "/workloads/"+w.ID, nil, nil) })
	return w
}

type status struct {
	Status string `json:"status"`
	Phase  string `json:"phase"`
	Image  string `json:"image"`
}

func (e *env) status(id string) (status, int) {
	e.t.Helper()
	var st status
	code, _ := e.call(http.MethodGet, "/workloads/"+id+"/status", nil, &st)
	return st, code
}

func (e *env) waitStatus(w workload, want string, limit time.Duration) {
	e.t.Helper()
	e.waitFor(limit, "conteneur "+want, func() (bool, error) {
		st, code := e.status(w.ID)
		if code != http.StatusOK {
			return false, nil
		}
		if st.Status != want && (st.Status == "FAILED" || st.Status == "DELETE_FAILED") {
			return false, fmt.Errorf("état %s (%s) au lieu de %s", st.Status, st.Phase, want)
		}
		return st.Status == want, nil
	})
}

// waitInstalling : Helm est lancé, il attend des pods qui ne seront jamais prêts.
func (e *env) waitInstalling(w workload) {
	e.t.Helper()
	e.waitFor(startingBefore, "installation en cours", func() (bool, error) {
		st, _ := e.status(w.ID)
		return st.Status == "STARTING" && st.Phase == "HELM_INSTALLING", nil
	})
	// L'étape est écrite juste avant l'appel à Helm.
	time.Sleep(5 * time.Second)
}

func (e *env) waitGone(w workload) {
	e.t.Helper()
	e.waitFor(workloadGone, "conteneur supprimé", func() (bool, error) {
		st, code := e.status(w.ID)
		if st.Status == "DELETE_FAILED" {
			return false, fmt.Errorf("suppression en échec : %s", st.Phase)
		}
		return code == http.StatusNotFound, nil
	})
}

func (e *env) waitFor(limit time.Duration, what string, done func() (bool, error)) {
	e.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		ok, err := done()
		require.NoError(e.t, err, what)
		if ok {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%s : pas atteint en %s", what, limit)
		}
		time.Sleep(pollEvery)
	}
}

// namespace : espace du projet, pour les contrôles dans le cluster.
func (e *env) namespace(w workload) string {
	e.t.Helper()
	var out struct {
		Namespace string `json:"Namespace"`
	}
	code, raw := e.call(http.MethodGet, "/workloads/"+w.ID, nil, &out)
	require.Equal(e.t, http.StatusOK, code, raw)
	return out.Namespace
}

// assertNothingLeft : ni release Helm ni Deployment : un reste consommerait le
// quota du projet, hors de la base. Sans E2E_KUBECONFIG, contrôle ignoré.
func (e *env) assertNothingLeft(namespace, release string) {
	e.t.Helper()
	if e.kube == nil {
		e.t.Log("E2E_KUBECONFIG absent : restes dans le cluster non contrôlés")
		return
	}
	ctx := context.Background()
	e.waitFor(workloadGone, "cluster nettoyé", func() (bool, error) {
		secrets, err := e.kube.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm,name=" + release})
		if err != nil {
			return false, err
		}
		pods, err := e.kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/instance=" + release})
		if err != nil {
			return false, err
		}
		return len(secrets.Items) == 0 && len(pods.Items) == 0, nil
	})
}

// shell : commande de l'environnement testé (arrêt du worker, par exemple).
func shell(t *testing.T, command string) {
	t.Helper()
	out, err := exec.Command("sh", "-c", command).CombinedOutput()
	require.NoError(t, err, string(out))
}

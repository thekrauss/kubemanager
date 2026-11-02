package e2e

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Parcours nominal : l'API et le worker pilotent le cluster, y compris à distance
// (E2E_KUBECONFIG désigne le cluster que le worker utilise).
func TestLifecycle(t *testing.T) {
	e := setup(t)
	project := e.newProject()
	w := e.deploy(project, e.image, imagePort)
	e.waitStatus(w, "RUNNING", workloadReady)
	namespace := e.namespace(w)

	if e.kube != nil {
		pods, err := e.kube.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/instance=" + w.Name})
		require.NoError(t, err)
		assert.NotEmpty(t, pods.Items, "pods dans le cluster attendu")
	}

	code, raw := e.call(http.MethodPost, "/workloads/"+w.ID+"/stop", nil, nil)
	require.Less(t, code, 300, raw)
	e.waitStatus(w, "STOPPED", workloadGone)

	code, raw = e.call(http.MethodPost, "/workloads/"+w.ID+"/start", nil, nil)
	require.Less(t, code, 300, raw)
	e.waitStatus(w, "RUNNING", workloadReady)

	code, raw = e.call(http.MethodDelete, "/workloads/"+w.ID, nil, nil)
	require.Equal(t, http.StatusAccepted, code, raw)
	e.waitGone(w)
	e.assertNothingLeft(namespace, w.Name)
}

// Suppression pendant une installation : réponse rapide, Helm interrompu, rien ne
// réapparaît, et le nom est de nouveau libre.
func TestDeleteDuringInstall(t *testing.T) {
	e := setup(t)
	project := e.newProject()
	w := e.deploy(project, e.image, closedPort)
	e.waitInstalling(w)
	namespace := e.namespace(w)

	start := time.Now()
	code, raw := e.call(http.MethodDelete, "/workloads/"+w.ID, nil, nil)
	require.Equal(t, http.StatusAccepted, code, raw)
	assert.Less(t, time.Since(start), deleteAnswersWithin)

	e.waitGone(w)
	e.assertNothingLeft(namespace, w.Name)

	// Plus aucun déploiement ne doit recréer la release après coup.
	time.Sleep(30 * time.Second)
	e.assertNothingLeft(namespace, w.Name)
}

// Une écriture pendant la suppression est refusée, sans erreur serveur.
func TestPatchDuringDelete(t *testing.T) {
	e := setup(t)
	project := e.newProject()
	w := e.deploy(project, e.image, imagePort)
	e.waitStatus(w, "RUNNING", workloadReady)

	code, raw := e.call(http.MethodDelete, "/workloads/"+w.ID, nil, nil)
	require.Equal(t, http.StatusAccepted, code, raw)
	code, raw = e.call(http.MethodPatch, "/workloads/"+w.ID, map[string]any{"replicas": 2}, nil)
	assert.Contains(t, []int{http.StatusConflict, http.StatusNotFound}, code, raw)
	e.waitGone(w)
}

// Application qui ne démarre jamais : une seule attente, puis FAILED. Corrigée, elle
// repart : la release en échec ne bloque pas le déploiement suivant.
func TestNotReadyFailsOnce(t *testing.T) {
	e := setup(t)
	project := e.newProject()
	w := e.deploy(project, e.image, closedPort)

	start := time.Now()
	e.waitFor(notReadyWithin, "conteneur FAILED", func() (bool, error) {
		st, _ := e.status(w.ID)
		return st.Status == "FAILED", nil
	})
	t.Logf("FAILED en %s", time.Since(start).Round(time.Second))

	code, raw := e.call(http.MethodPatch, "/workloads/"+w.ID, map[string]any{"target_port": imagePort}, nil)
	require.Equal(t, http.StatusAccepted, code, raw)
	e.waitStatus(w, "RUNNING", workloadReady)
}

// Worker tué en pleine installation : Temporal relance l'activité sur le worker
// redémarré, la release restée « pending » ne bloque rien et l'état final s'écrit.
func TestWorkerKilledDuringInstall(t *testing.T) {
	e := setup(t)
	kill, restart := os.Getenv("E2E_WORKER_KILL"), os.Getenv("E2E_WORKER_START")
	if kill == "" || restart == "" {
		t.Skip("E2E_WORKER_KILL et E2E_WORKER_START requis")
	}
	project := e.newProject()
	w := e.deploy(project, e.image, closedPort)
	e.waitInstalling(w)

	shell(t, kill)
	// Au-delà du délai de heartbeat : Temporal tient la tentative pour morte.
	time.Sleep(20 * time.Second)
	shell(t, restart)

	e.waitFor(notReadyWithin+time.Minute, "état final après la panne", func() (bool, error) {
		st, _ := e.status(w.ID)
		return st.Status == "FAILED", nil
	})

	code, raw := e.call(http.MethodPatch, "/workloads/"+w.ID, map[string]any{"target_port": imagePort}, nil)
	require.Equal(t, http.StatusAccepted, code, raw, "release bloquée après la panne ?")
	e.waitStatus(w, "RUNNING", workloadReady)
}

// Deux PATCH simultanés : un seul déploiement part, l'autre reçoit 409, et la
// demande refusée ne laisse aucune trace, variables comprises.
func TestConcurrentPatches(t *testing.T) {
	e := setup(t)
	project := e.newProject()
	w := e.deploy(project, e.image, imagePort)
	e.waitStatus(w, "RUNNING", workloadReady)

	const value = "WHO"
	type result struct {
		who  string
		code int
		raw  string
	}
	for round := range 5 {
		results := make(chan result, 2)
		var wg sync.WaitGroup
		for _, who := range []string{"a", "b"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				code, raw := e.call(http.MethodPatch, "/workloads/"+w.ID, map[string]any{"env_vars": map[string]string{value: who}}, nil)
				results <- result{who, code, raw}
			}()
		}
		wg.Wait()
		close(results)

		var accepted []string
		for r := range results {
			switch r.code {
			case http.StatusAccepted:
				accepted = append(accepted, r.who)
			case http.StatusConflict:
				assert.Contains(t, r.raw, "DEPLOY_IN_PROGRESS", "tour %d", round)
			default:
				t.Errorf("tour %d, demande %s : %d %s", round, r.who, r.code, r.raw)
			}
		}
		require.NotEmpty(t, accepted, "tour %d : aucune demande acceptée", round)
		e.waitStatus(w, "RUNNING", workloadReady)

		// Deux acceptations : l'une a fini avant l'autre, l'ordre n'est pas connu.
		if len(accepted) != 1 {
			continue
		}
		var vars struct {
			Variables []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"variables"`
		}
		code, raw := e.call(http.MethodGet, "/workloads/"+w.ID+"/variables", nil, &vars)
		require.Equal(t, http.StatusOK, code, raw)
		for _, v := range vars.Variables {
			if v.Name == value {
				assert.Equal(t, accepted[0], v.Value, "tour %d : la demande refusée a écrasé la variable", round)
			}
		}
	}
}

// Build depuis un dépôt Git public, envoi au registre, puis lancement de l'image.
func TestBuildFromGit(t *testing.T) {
	e := setup(t)
	gitURL := os.Getenv("E2E_GIT_URL")
	if gitURL == "" {
		t.Skip("E2E_GIT_URL requis")
	}
	project := e.newProject()
	var build struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Image  string `json:"image"`
	}
	code, raw := e.call(http.MethodPost, "/projects/"+project+"/builds", map[string]string{
		"name": "e2e", "git_url": gitURL, "git_ref": os.Getenv("E2E_GIT_REF"),
	}, &build)
	require.Equal(t, http.StatusAccepted, code, raw)

	e.waitFor(buildDone, "build terminé", func() (bool, error) {
		code, raw := e.call(http.MethodGet, "/projects/"+project+"/builds/"+build.ID, nil, &build)
		require.Equal(t, http.StatusOK, code, raw)
		require.NotContains(t, []string{"FAILED", "CANCELLED"}, build.Status, raw)
		return build.Status == "SUCCEEDED", nil
	})
	require.NotEmpty(t, build.Image)

	w := e.deploy(project, build.Image, imagePort)
	e.waitStatus(w, "RUNNING", workloadReady)
}

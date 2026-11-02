package activities

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func testConfig() JobConfig {
	return JobConfig{Namespace: "betorun-builds", Image: "moby/buildkit:v1-rootless", CPURequest: "250m", CPULimit: "2",
		MemoryRequest: "512Mi", MemoryLimit: "4Gi", DiskLimit: "10Gi", DeadlineSecond: 1800, EgressAllow: []string{"172.21.0.1/32"}}
}

// Le pod d'un build exécute du code arbitraire : aucun accès à l'API
// Kubernetes, utilisateur non privilégié, durée et ressources bornées.
func TestNewJob_Hardened(t *testing.T) {
	job := newJob(testConfig(), JobSpec{BuildID: "b1", ProjectID: "p1", GitURL: "https://x/y.git", Dockerfile: "Dockerfile; rm -rf /",
		Image: "reg/p1/web:b", CacheRef: "reg/p1/web:buildcache", AuthSecret: "s"})
	pod := job.Spec.Template.Spec
	c := pod.Containers[0]

	assert.False(t, *pod.AutomountServiceAccountToken)
	assert.False(t, *pod.EnableServiceLinks)
	assert.True(t, *pod.SecurityContext.RunAsNonRoot)
	assert.NotZero(t, *pod.SecurityContext.RunAsUser)
	assert.Zero(t, *job.Spec.BackoffLimit)
	assert.EqualValues(t, 1800, *job.Spec.ActiveDeadlineSeconds)
	assert.False(t, c.Resources.Limits.Cpu().IsZero())
	assert.False(t, c.Resources.Limits.Memory().IsZero())
	assert.False(t, c.Resources.Limits.StorageEphemeral().IsZero())
	require.NotNil(t, pod.Volumes[0].EmptyDir.SizeLimit)
	assert.True(t, c.VolumeMounts[1].ReadOnly, "identifiants du registre en lecture seule")

	// Valeurs utilisateur : variables d'environnement, jamais dans le script.
	assert.NotContains(t, strings.Join(c.Command, " "), "Dockerfile; rm -rf /")
	assert.Contains(t, buildScript, `"filename=$DF_NAME"`)
}

func TestNewNetworkPolicy_BlocksInternal(t *testing.T) {
	np := newNetworkPolicy(testConfig())
	require.Len(t, np.Spec.PolicyTypes, 2, "entrée et sortie")
	assert.Empty(t, np.Spec.Ingress, "aucune connexion entrante")
	internet := np.Spec.Egress[1].To[0].IPBlock
	assert.Equal(t, "0.0.0.0/0", internet.CIDR)
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"} {
		assert.Contains(t, internet.Except, cidr)
	}
	assert.Equal(t, "172.21.0.1/32", np.Spec.Egress[2].To[0].IPBlock.CIDR, "exception explicite")
}

// Le jeton de clonage n'arrive que par référence au Secret : jamais de valeur
// dans la spec du Job.
func TestNewJob_GitTokenBySecretRef(t *testing.T) {
	cfg := JobConfig{Namespace: "b", Image: "i", CPURequest: "1", CPULimit: "1", MemoryRequest: "1Mi", MemoryLimit: "1Mi", DiskLimit: "1Mi", DeadlineSecond: 60}
	job := newJob(cfg, JobSpec{BuildID: "x", AuthSecret: "auth", GitURL: "https://github.com/a/b.git", GitCommit: "abc", GitTokenSecret: "build-git-x"})
	var ref *corev1.SecretKeySelector
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "BUILD_GIT_TOKEN" {
			assert.Empty(t, e.Value)
			require.NotNil(t, e.ValueFrom)
			ref = e.ValueFrom.SecretKeyRef
		}
	}
	require.NotNil(t, ref)
	assert.Equal(t, "build-git-x", ref.Name)

	job = newJob(cfg, JobSpec{BuildID: "x", AuthSecret: "auth", GitURL: "https://github.com/a/b.git"})
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		assert.NotEqual(t, "BUILD_GIT_TOKEN", e.Name, "dépôt public : pas de jeton")
	}
}

// Le script n'écrit le jeton nulle part : ni URL, ni trace d'exécution.
func TestBuildScript_TokenOnlyInHeader(t *testing.T) {
	assert.Contains(t, mainScript, "http.extraHeader")
	assert.NotContains(t, mainScript, "set -x")
	assert.NotContains(t, mainScript, "@github")
	for _, line := range strings.Split(mainScript, "\n") {
		if strings.Contains(line, "echo") {
			assert.NotContains(t, line, "BUILD_GIT_TOKEN")
			assert.NotContains(t, line, "GIT_CONFIG_VALUE")
		}
	}
}

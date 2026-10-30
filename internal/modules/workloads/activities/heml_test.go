package activities

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildHelmValues_DefaultRootFSWritable(t *testing.T) {
	vals := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", TargetPort: 8080})

	sec := vals["containerSecurityContext"].(map[string]any)
	assert.Equal(t, false, sec["readOnlyRootFilesystem"])
	assert.Equal(t, false, vals["tmpfs"].(map[string]any)["enabled"])
}

func TestBuildHelmValues_ReadOnlyRootFSMountsTmpfs(t *testing.T) {
	vals := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", ReadOnlyRootFS: true})

	sec := vals["containerSecurityContext"].(map[string]any)
	assert.Equal(t, true, sec["readOnlyRootFilesystem"])
	assert.Equal(t, true, vals["tmpfs"].(map[string]any)["enabled"])
}

func TestBuildHelmValues_EgressPublicPorts(t *testing.T) {
	vals := buildHelmValues(InstallWorkloadInput{ReleaseName: "api"})

	egress := vals["networkPolicy"].(map[string]any)["egress"].(map[string]any)
	ports := egress["publicPorts"].([]int)
	assert.Contains(t, ports, 443)
	assert.Contains(t, ports, 5432)
	assert.NotContains(t, ports, 25)
}

func TestBuildHelmValues_WorkloadIdentity(t *testing.T) {
	none := buildHelmValues(InstallWorkloadInput{ReleaseName: "api"})
	if none["betorunIdentity"].(map[string]any)["enabled"] != false {
		t.Fatal("sans compte de service : pas d'identité")
	}
	vals := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", Identity: &WorkloadIdentity{
		ServiceAccount: "serviceAccount:x", Audience: "https://api/kmanager/v1/iam", TokenURI: "https://api/kmanager/v1/iam/token"}})
	id := vals["betorunIdentity"].(map[string]any)
	if id["enabled"] != true || id["audience"] != "https://api/kmanager/v1/iam" || id["serviceAccount"] != "serviceAccount:x" {
		t.Fatalf("identité : %v", id)
	}
	// Le STS retrouve le workload par system:serviceaccount:<ns>:<release>.
	if vals["serviceAccount"].(map[string]any)["name"] != "api" {
		t.Fatalf("serviceAccount : %v", vals["serviceAccount"])
	}
}

// Sans URL publique (IP du point d'entrée non configurée) : pas d'ingress
// (avant : hôte « web-xxxxxx..sslip.io » refusé, déploiement en échec).
func TestBuildHelmValues_Ingress(t *testing.T) {
	none := buildHelmValues(InstallWorkloadInput{ReleaseName: "api"})
	assert.Equal(t, false, none["ingress"].(map[string]any)["enabled"])

	vals := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", ExternalURL: "api-123456.1.2.3.4.sslip.io"})
	ing := vals["ingress"].(map[string]any)
	assert.Equal(t, true, ing["enabled"])
	assert.Equal(t, "api-123456.1.2.3.4.sslip.io", ing["host"])
	assert.Equal(t, "nginx", ing["className"])

	assert.Equal(t, "ingress-nginx", vals["networkPolicy"].(map[string]any)["ingressNSLabelValue"])

	traefik := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", ExternalURL: "h",
		IngressClass: "traefik", IngressNamespace: "kube-system"})
	assert.Equal(t, "traefik", traefik["ingress"].(map[string]any)["className"])
	assert.Equal(t, "kube-system", traefik["networkPolicy"].(map[string]any)["ingressNSLabelValue"])
}

// Variables : jamais en clair dans les valeurs Helm (donc dans la spec du
// Deployment) ; l'empreinte change avec leur contenu, pas avec leur ordre.
func TestBuildHelmValues_EnvOnlyThroughSecret(t *testing.T) {
	a := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", Env: map[string]string{"A": "1", "B": "2"}})
	_, plain := a["envVars"]
	assert.False(t, plain, "variables absentes des valeurs Helm")
	assert.Equal(t, "api-env", a["envSecretName"])

	same := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", Env: map[string]string{"B": "2", "A": "1"}})
	assert.Equal(t, a["envChecksum"], same["envChecksum"])
	changed := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", Env: map[string]string{"A": "1", "B": "3"}})
	assert.NotEqual(t, a["envChecksum"], changed["envChecksum"], "valeur modifiée : nouveaux pods")
}

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

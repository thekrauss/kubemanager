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

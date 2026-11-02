package activities

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildHelmValues_CustomDomains(t *testing.T) {
	v := buildHelmValues(InstallWorkloadInput{ReleaseName: "api", CustomDomains: []string{"a.sn", "b.sn"}, CustomDomainTLS: true})
	assert.Equal(t, []any{"a.sn", "b.sn"}, v["customDomains"])
	assert.Equal(t, true, v["customDomainTLS"])
	assert.Empty(t, buildHelmValues(InstallWorkloadInput{ReleaseName: "api"})["customDomains"])
}

// Une limite basse ne doit pas échouer à cause de la réservation par défaut.
func TestRequestWithin(t *testing.T) {
	assert.Equal(t, "64Mi", requestWithin("", "128Mi", "64Mi"))
	assert.Equal(t, "128Mi", requestWithin("", "128Mi", "512Mi"))
	assert.Equal(t, "128Mi", requestWithin("", "128Mi", ""))
	assert.Equal(t, "32Mi", requestWithin("32Mi", "128Mi", "64Mi"), "valeur explicite conservée")
	assert.Equal(t, "50m", requestWithin("", "100m", "50m"))
}

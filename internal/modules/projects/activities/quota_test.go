package activities

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Marge pour le pod supplémentaire des mises à jour progressives.
func TestWithRollingMargin(t *testing.T) {
	for in, want := range map[string]string{"2": "4", "500m": "1", "4Gi": "8Gi", "1536Mi": "3Gi"} {
		got := withRollingMargin(resource.MustParse(in))
		assert.Zero(t, got.Cmp(resource.MustParse(want)), "%s → %s", in, got.String())
	}
}

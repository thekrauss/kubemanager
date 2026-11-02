package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Le stockage utilisé est compté en Mi, comme les quotas du service.
func TestParseSizeToMi(t *testing.T) {
	r := &workloadRepository{}
	assert.EqualValues(t, 1024, r.parseSizeToMi("1Gi"))
	assert.EqualValues(t, 512, r.parseSizeToMi("512Mi"))
	assert.Zero(t, r.parseSizeToMi("n'importe quoi"))
}

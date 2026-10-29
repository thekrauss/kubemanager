package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Fix P1 : les events critiques sont TOUJOURS loggés, les events normaux sont samplés.
func TestAuditSampling_CriticalEventsAlwaysLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	base := zap.New(core)
	// sampleRate=1000 → quasi aucun event "success" ne devrait passer.
	al := NewAuditLoggerWithSampling(base, 1000)

	for i := 0; i < 50; i++ {
		al.LogEvent(AuditAuthFailed, zap.Int("i", i))
		al.LogEvent(AuditTokenRevoked)
		al.LogEvent(AuditPermissionDenied)
		al.LogEvent(AuditAPIKeyCreated)
	}

	// 50 × 4 = 200 events critiques, tous loggés.
	assert.Equal(t, 200, logs.Len(), "events critiques doivent tous être loggés")
}

func TestAuditSampling_NonCriticalEventsSampled(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	base := zap.New(core)
	// sampleRate=10 → ~10% des events success loggés.
	al := NewAuditLoggerWithSampling(base, 10)

	const n = 10_000
	for i := 0; i < n; i++ {
		al.LogEvent(AuditAuthSuccess)
	}

	count := logs.Len()
	// Tolérance large : on attend ~1000 (10%), mais rand peut varier.
	// On vérifie juste que c'est nettement en dessous de n ET non-nul.
	assert.Greater(t, count, n/20, "sampling doit laisser passer quelques events")
	assert.Less(t, count, n/2, "sampling doit réduire significativement le volume")
}

func TestAuditSampling_Disabled_LogsEverything(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	base := zap.New(core)
	// sampleRate=1 → tout loggé.
	al := NewAuditLoggerWithSampling(base, 1)

	for i := 0; i < 100; i++ {
		al.LogEvent(AuditAuthSuccess)
	}
	assert.Equal(t, 100, logs.Len())
}

func TestAuditSampling_DefaultNoSampling(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	base := zap.New(core)
	al := NewAuditLogger(base) // constructeur legacy — pas de sampling

	for i := 0; i < 100; i++ {
		al.LogEvent(AuditAuthSuccess)
	}
	assert.Equal(t, 100, logs.Len(), "legacy constructor doit garder le comportement sans sampling")
}

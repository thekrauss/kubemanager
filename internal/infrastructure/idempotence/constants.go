package idempotency

import "time"

// DefaultRecordTTL : durée de conservation d'un résultat idempotent.
const DefaultRecordTTL = 12 * time.Hour

// Clés Redis.
const (
	// recordKeyFormat : idempotency:<clé>.
	recordKeyFormat = "idempotency:%s"
	// activityKeyFormat : tmp:<workflow>:<run>:<type d'activity>:<activity>.
	activityKeyFormat = "tmp:%s:%s:%s:%s"
)

// casMaxAttempts : tentatives d'un compare-and-swap (WATCH Redis) en conflit.
const casMaxAttempts = 3

// Étapes de finalisation signalées à OnFinalizeError.
const (
	finalizeStepFail     = "fail"
	finalizeStepComplete = "complete"
	finalizeStepStore    = "store"
)

// finalizeTimeout : libération du verrou ou enregistrement du résultat, sur un
// contexte détaché de celui de l'activité (possiblement annulé).
const finalizeTimeout = 5 * time.Second

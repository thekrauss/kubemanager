package service

import "time"

// Statuts d'une clé d'API dans le journal (APIKeyAuditEntry.Status).
const (
	apiKeyStatusActive  = "active"
	apiKeyStatusRevoked = "revoked"
	apiKeyStatusExpired = "expired"
)

// Format des clés d'API : km_<env>_<prefix>_<secret>.
const (
	apiKeyBrand     = "km"
	apiKeySeparator = "_"
	// apiKeyPrefixParts : segments de la partie publique (km, env, prefix).
	apiKeyPrefixParts = 3
)

// Accumulateur des compteurs d'usage des clés d'API.
const (
	DefaultUsageFlushInterval = 5 * time.Second
	DefaultUsageMaxPending    = 500
	// usageFlushTimeout : budget d'un flush en base.
	usageFlushTimeout = 5 * time.Second
	// usageAccumulatorComponent : nom du composant dans les logs.
	usageAccumulatorComponent = "apikey-usage-accumulator"
)

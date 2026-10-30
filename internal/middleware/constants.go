package middleware

import "time"

// Clés d'API (APIKeyAuth).
const (
	// apiKeyLookupPrefixLen : caractères de la clé servant à la retrouver en base.
	apiKeyLookupPrefixLen = 7
	// apiKeyAuthScheme : schéma « Authorization: ApiKey <clé> ».
	apiKeyAuthScheme = "ApiKey"
	// apiKeyUsageUpdateTimeout : mise à jour asynchrone des compteurs d'usage.
	apiKeyUsageUpdateTimeout = 5 * time.Second
)

// Codes d'erreur renvoyés par le limiteur de débit.
const (
	errCodeRateLimitBackendUnavailable = "RATE_LIMIT_BACKEND_UNAVAILABLE"
	errCodeRateLimitExceeded           = "RATE_LIMIT_EXCEEDED"
)

// Valeurs des en-têtes de sécurité posés sur toutes les réponses de l'API.
const (
	secureFrameOptions          = "DENY"
	secureXSSProtection         = "1; mode=block"
	secureHSTS                  = "max-age=31536000; includeSubDomains; preload"
	secureReferrerPolicy        = "strict-origin-when-cross-origin"
	secureContentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'"
	securePermissionsPolicy     = "camera=(), microphone=(), geolocation=(), payment=()"
)

// Idempotence des requêtes mutantes.
const (
	// idempotencyKeyFormat : idem:<méthode>:<route>:<principal>:<clé>.
	idempotencyKeyFormat = "idem:%s:%s:%s:%s"
	idempotencyErrorKey  = "idempotency_error"

	idempotencyErrPayloadMismatch = "payload_mismatch"
	idempotencyErrInProgress      = "in_progress"

	idempotencyStatusHit  = "hit"
	idempotencyStatusMiss = "miss"
)

const (
	// idempotencyProcessingTTL : durée de la marque « en cours » d'une requête.
	idempotencyProcessingTTL = 2 * time.Minute
	// idempotencyRetryAfter : Retry-After (secondes) d'une requête en cours.
	idempotencyRetryAfter = "5"
)

// Limiteur de débit.
const (
	// rateLimitKeyFormat : rl:<ip>:<début de fenêtre unix>.
	rateLimitKeyFormat = "rl:%s:%d"
	// defaultRateLimitWindow : fenêtre si la configuration n'en donne pas.
	defaultRateLimitWindow = time.Minute
	// rateLimitBackendTimeout : budget de l'appel Redis par requête.
	rateLimitBackendTimeout = 250 * time.Millisecond
)

// Rafraîchissement des clés publiques (JWKS) du fournisseur d'identité.
const (
	jwksRefreshTimeout   = 10 * time.Second
	jwksRefreshInterval  = 6 * time.Hour
	jwksRefreshRateLimit = 5 * time.Minute
)

// jsonContentMarker : sous-chaîne d'un Content-Type JSON (traduction des messages).
const jsonContentMarker = "json"

// messageField : champ JSON des messages traduits dans les réponses réussies.
const messageField = "message"

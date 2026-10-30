package iamapi

// Paramètres du formulaire de l'endpoint token (RFC 6749, 7523, 8693).
const (
	formGrantType        = "grant_type"
	formAssertion        = "assertion"
	formSubjectToken     = "subject_token"
	formSubjectTokenType = "subject_token_type"
	// formServiceAccount : extension betorun, compte visé par l'échange fédéré.
	formServiceAccount = "service_account"
)

// Codes d'erreur OAuth (RFC 6749 §5.2) renvoyés hors GrantError.
const (
	oauthErrInvalidRequest         = "invalid_request"
	oauthErrUnsupportedGrantType   = "unsupported_grant_type"
	oauthErrServerError            = "server_error"
	oauthErrTemporarilyUnavailable = "temporarily_unavailable"
)

// Cache-Control des métadonnées publiques du STS.
const (
	cacheControlJWKS      = "public, max-age=300"
	cacheControlDiscovery = "public, max-age=3600"
	// authMethodNone : le endpoint token n'exige aucune authentification client.
	authMethodNone = "none"
)

// keycloakAccountSecurityPath : page « Sécurité » de la console de compte
// Keycloak (retirée avec l'authentification gérée par betorun).
const keycloakAccountSecurityPath = "/account/#/account-security/signing-in"

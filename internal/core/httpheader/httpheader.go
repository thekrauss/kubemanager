// Package httpheader : noms d'en-têtes HTTP, types de contenu et directives
// de cache utilisés par l'API, l'edge des sites statiques et la CLI.
package httpheader

// En-têtes standard.
const (
	Accept             = "Accept"
	AcceptLanguage     = "Accept-Language"
	Allow              = "Allow"
	Authorization      = "Authorization"
	CacheControl       = "Cache-Control"
	ContentLanguage    = "Content-Language"
	ContentLength      = "Content-Length"
	ContentType        = "Content-Type"
	ETag               = "ETag"
	Pragma             = "Pragma"
	RetryAfter         = "Retry-After"
	UserAgent          = "User-Agent"
	Vary               = "Vary"
	XContentTypeOpts   = "X-Content-Type-Options"
	XFrameOptions      = "X-Frame-Options"
	XXSSProtection     = "X-XSS-Protection"
	StrictTransportSec = "Strict-Transport-Security"
	ReferrerPolicy     = "Referrer-Policy"
	ContentSecPolicy   = "Content-Security-Policy"
	PermissionsPolicy  = "Permissions-Policy"
)

// En-têtes propres à la plateforme.
const (
	// XAPIKey : clé d'API (clients, watcher, Centrifugo).
	XAPIKey = "X-API-Key"
	// IdempotencyKey / IdempotencyStatus : rejeu des requêtes mutantes.
	IdempotencyKey    = "Idempotency-Key"
	IdempotencyStatus = "Idempotency-Status"
	// XTenantID / XProjectID : projet ciblé quand il n'est pas dans le chemin.
	XTenantID  = "X-Tenant-ID"
	XProjectID = "X-Project-ID"
)

// Types de contenu.
const (
	MIMEJSON           = "application/json"
	MIMEJSONUTF8       = "application/json; charset=utf-8"
	MIMEFormURLEncoded = "application/x-www-form-urlencoded"
	MIMEOctetStream    = "application/octet-stream"
	MIMEZip            = "application/zip"
	MIMETextPlainUTF8  = "text/plain; charset=utf-8"
	MIMETextHTMLUTF8   = "text/html; charset=utf-8"
)

// Directives Cache-Control.
const (
	NoStore = "no-store"
	NoCache = "no-cache"
	NoSniff = "nosniff"
)

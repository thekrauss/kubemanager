package middleware

import (
	"github.com/gin-gonic/gin"
)

// SecureHeaders adds defence-in-depth HTTP response headers.
// Place it early in the middleware chain (right after recovery).
func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()

		// Clickjacking protection
		h.Set("X-Frame-Options", "DENY")

		// MIME-sniffing protection
		h.Set("X-Content-Type-Options", "nosniff")

		// XSS reflection filter (legacy browsers)
		h.Set("X-XSS-Protection", "1; mode=block")

		// HSTS — force HTTPS for 1 year, include subdomains
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")

		// Referrer policy — send origin only on cross-origin, full on same-origin
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Content Security Policy — API server, block everything except self
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")

		// Prevent browsers from caching API responses with auth data
		h.Set("Cache-Control", "no-store")

		// Permissions policy — disable sensitive browser features
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")

		c.Next()
	}
}

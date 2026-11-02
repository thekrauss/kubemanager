package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/core/httpheader"
)

// SecureHeaders adds defence-in-depth HTTP response headers.
// Place it early in the middleware chain (right after recovery).
func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()

		// Clickjacking protection
		h.Set(httpheader.XFrameOptions, secureFrameOptions)

		// MIME-sniffing protection
		h.Set(httpheader.XContentTypeOpts, httpheader.NoSniff)

		// XSS reflection filter (legacy browsers)
		h.Set(httpheader.XXSSProtection, secureXSSProtection)

		// HSTS — force HTTPS for 1 year, include subdomains
		h.Set(httpheader.StrictTransportSec, secureHSTS)

		// Referrer policy — send origin only on cross-origin, full on same-origin
		h.Set(httpheader.ReferrerPolicy, secureReferrerPolicy)

		// Content Security Policy — API server, block everything except self
		h.Set(httpheader.ContentSecPolicy, secureContentSecurityPolicy)

		// Prevent browsers from caching API responses with auth data
		h.Set(httpheader.CacheControl, httpheader.NoStore)

		// Permissions policy — disable sensitive browser features
		h.Set(httpheader.PermissionsPolicy, securePermissionsPolicy)

		c.Next()
	}
}

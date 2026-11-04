package httpguard

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders sets the headers every API response should carry. The API
// returns JSON only, so the content security policy forbids everything, and
// responses are never cached because they depend on who is signed in.
// HSTS is sent only in production and only for requests that arrived over TLS
// (directly, or through a trusted proxy that says so).
func SecurityHeaders(production bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.Writer.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Resource-Policy", "same-site")
		header.Set("Cache-Control", "no-store")
		if production && (c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")) {
			header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

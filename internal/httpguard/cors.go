// Package httpguard holds the HTTP protections a public deployment needs: an
// origin allowlist, security headers, rate limits, a login lockout, request IDs
// with structured access logs, and request size limits. It knows nothing about
// the application's handlers.
package httpguard

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// DevOrigins are the origins of the local Vite dev server. They are only used
// when no allowlist is configured outside production.
func DevOrigins() []string {
	origins := make([]string, 0, 8)
	for _, host := range []string{"localhost", "127.0.0.1"} {
		for _, port := range []string{"5173", "5174", "5175", "5176"} {
			origins = append(origins, "http://"+host+":"+port)
		}
	}
	return origins
}

// ParseOrigins turns a comma separated list into a clean allowlist. Anything that
// is not a bare http(s) origin is dropped, and so is "*": credentials are sent with
// every request, so a wildcard would let any site act as the signed-in user.
func ParseOrigins(raw string) []string {
	var origins []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimRight(strings.TrimSpace(item), "/")
		parsed, err := url.Parse(item)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.User != nil || parsed.RawQuery != "" {
			continue
		}
		origins = append(origins, parsed.Scheme+"://"+strings.ToLower(parsed.Host))
	}
	return origins
}

// CORS allows cross-origin requests from exactly the listed origins. A request
// from any other origin gets no CORS headers, so the browser refuses to expose the
// response, and a preflight from it is answered with 403.
func CORS(origins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[origin] = true
	}
	return func(c *gin.Context) {
		c.Writer.Header().Add("Vary", "Origin")
		origin := c.GetHeader("Origin")
		isAllowed := origin != "" && allowed[origin]
		if isAllowed {
			header := c.Writer.Header()
			header.Set("Access-Control-Allow-Origin", origin)
			header.Set("Access-Control-Allow-Credentials", "true")
			header.Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")
		}
		if c.Request.Method != http.MethodOptions {
			c.Next()
			return
		}
		if origin != "" && c.GetHeader("Access-Control-Request-Method") != "" {
			if !isAllowed {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			header := c.Writer.Header()
			header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, X-Request-ID")
			header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			header.Set("Access-Control-Max-Age", "600")
		}
		c.AbortWithStatus(http.StatusNoContent)
	}
}

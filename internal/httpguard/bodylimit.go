package httpguard

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// BodyLimit caps request bodies: jsonMax for ordinary requests and uploadMax for
// multipart ones (archives, skill packages). A request that announces a larger
// body is refused at once; one that lies about its size is cut off while being read.
func BodyLimit(jsonMax, uploadMax int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := jsonMax
		if strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/") {
			limit = uploadMax
		}
		if c.Request.ContentLength > limit {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"ok": false, "message": "Permintaan terlalu besar."})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

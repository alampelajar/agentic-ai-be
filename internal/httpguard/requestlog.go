package httpguard

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
)

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// RequestID gives every request an ID, returned in X-Request-ID and available to
// handlers as c.GetString("request_id"). An ID sent by a client (or the proxy in
// front) is kept only when it is a plain token, so it cannot inject log lines.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if !validRequestID.MatchString(id) {
			buffer := make([]byte, 12)
			_, _ = rand.Read(buffer)
			id = hex.EncodeToString(buffer)
		}
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// AccessLog writes one structured line per request. It logs the route pattern
// (/api/workspaces/:id), never the raw path or the query string, because those
// carry identifiers and sometimes tokens; and never headers or bodies.
func AccessLog(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		level := slog.LevelInfo
		switch status := c.Writer.Status(); {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case status >= http.StatusBadRequest:
			level = slog.LevelWarn
		}
		attrs := []any{
			"request_id", c.GetString("request_id"),
			"method", c.Request.Method,
			"route", route,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(started).Milliseconds(),
			"bytes", c.Writer.Size(),
			"ip", ClientKey(c.ClientIP()),
		}
		if id, ok := c.Get("user_id"); ok {
			attrs = append(attrs, "user_id", id)
		}
		logger.Log(c.Request.Context(), level, "request", attrs...)
	}
}

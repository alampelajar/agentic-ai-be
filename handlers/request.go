package handlers

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// getCurrentUserID is the signed-in user's id, set by the auth middleware.
func getCurrentUserID(c *gin.Context) (uint, bool) {
	value, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}

	switch id := value.(type) {
	case uint:
		if id == 0 {
			return 0, false
		}
		return id, true

	case int:
		if id <= 0 {
			return 0, false
		}
		return uint(id), true

	case int64:
		if id <= 0 {
			return 0, false
		}
		return uint(id), true

	case float64:
		if id <= 0 {
			return 0, false
		}
		return uint(id), true
	}

	return 0, false
}

// idParam reads a positive numeric ":id" from the URL.
func idParam(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 || id > uint64(^uint(0)) {
		return 0, false
	}
	return uint(id), true
}

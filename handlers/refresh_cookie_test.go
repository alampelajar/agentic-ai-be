package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func refreshCookieHeader(t *testing.T, env string) string {
	t.Helper()
	t.Setenv("APP_ENV", env)
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	setRefreshCookie(c, adminRefresh, "token-value", refreshCookieMaxAge)
	return response.Header().Get("Set-Cookie")
}

func TestRefreshCookieAttributes(t *testing.T) {
	header := refreshCookieHeader(t, "production")
	for _, want := range []string{"admin_refresh_token=token-value", "Path=/api/admin", "HttpOnly", "Secure", "SameSite=Strict", "Max-Age=604800"} {
		if !strings.Contains(header, want) {
			t.Errorf("production Set-Cookie %q lacks %q", header, want)
		}
	}
	// Local development runs on plain http://localhost, where a Secure cookie would not be kept.
	if header := refreshCookieHeader(t, ""); strings.Contains(header, "Secure") || !strings.Contains(header, "SameSite=Strict") {
		t.Errorf("development Set-Cookie = %q", header)
	}
}

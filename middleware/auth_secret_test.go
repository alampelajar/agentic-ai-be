package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"agentic-ai-backend/utils"
)

// With no key set, a token signed with the empty key must not get through. The request
// is refused before the database is touched.
func TestAuthRefusesTokensWhenTheKeyIsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_ACCESS_SECRET", "")
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, utils.Claims{UserID: 1, Role: "admin"}).SignedString([]byte(""))

	router := gin.New()
	reached := false
	router.GET("/x", AuthMiddleware(), func(c *gin.Context) { reached = true })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+forged)
	router.ServeHTTP(w, req)

	if reached || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, handler reached %v", w.Code, reached)
	}
}

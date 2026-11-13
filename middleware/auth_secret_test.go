package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"agentic-ai-backend/utils"
)

// Without a configured secret, a token signed with an empty key (which anyone can
// make, with any role) must be refused before the database is ever asked.
func TestAuthMiddlewareRefusesTokensWhenSecretIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_ACCESS_SECRET", "")
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, utils.Claims{UserID: 1, Email: "x@example.test", Role: "admin"}).SignedString([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	reached := false
	router.GET("/admin", AuthMiddleware(), AdminMiddleware(), func(c *gin.Context) { reached = true })
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.Header.Set("Authorization", "Bearer "+forged)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if reached || response.Code == http.StatusOK {
		t.Fatalf("a forged token got through: status %d", response.Code)
	}
}

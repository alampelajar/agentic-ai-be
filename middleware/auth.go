package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"agentic-ai-backend/utils"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"ok":      false,
				"message": "Access token wajib dikirim",
			})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)

		if len(parts) != 2 ||
			parts[0] != "Bearer" ||
			parts[1] == "" {

			c.JSON(http.StatusUnauthorized, gin.H{
				"ok":      false,
				"message": "Format Authorization tidak valid",
			})
			c.Abort()
			return
		}

		accessToken := parts[1]
		secret := os.Getenv("JWT_ACCESS_SECRET")

		token, err := jwt.ParseWithClaims(
			accessToken,
			&utils.Claims{},
			func(token *jwt.Token) (interface{}, error) {
				if token.Method != jwt.SigningMethodHS256 {
					return nil, jwt.ErrSignatureInvalid
				}

				return []byte(secret), nil
			},
		)

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"ok":      false,
				"message": "Access token tidak valid atau sudah expired",
			})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(*utils.Claims)

		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"ok":      false,
				"message": "Claims token tidak valid",
			})
			c.Abort()
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("user_email", claims.Email)
		c.Set("user_role", claims.Role)

		c.Next()
	}
}

// AdminMiddleware memastikan hanya user dengan role admin
// yang dapat mengakses endpoint admin.
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		roleValue, exists := c.Get("user_role")

		if !exists {
			c.JSON(http.StatusForbidden, gin.H{
				"ok":      false,
				"message": "Role pengguna tidak ditemukan",
			})
			c.Abort()
			return
		}

		role, ok := roleValue.(string)

		if !ok || role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{
				"ok":      false,
				"message": "Akses hanya untuk admin",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

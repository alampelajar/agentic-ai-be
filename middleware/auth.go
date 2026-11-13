package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
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
		secret, secretErr := utils.AccessSecret()
		if secretErr != nil {
			// Never check a token against an empty key: anyone could have signed it.
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"ok":      false,
				"message": "Server belum dikonfigurasi dengan benar",
			})
			c.Abort()
			return
		}

		token, err := jwt.ParseWithClaims(
			accessToken,
			&utils.Claims{},
			func(token *jwt.Token) (interface{}, error) {
				if token.Method != jwt.SigningMethodHS256 {
					return nil, jwt.ErrSignatureInvalid
				}

				return secret, nil
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

		// Reject still-unexpired JWTs for accounts that have been deleted.
		var activeUser models.User
		if err := config.DB.Select("id", "email", "role").First(&activeUser, claims.UserID).Error; err != nil {
			status := http.StatusUnauthorized
			message := "Akun pengguna sudah tidak tersedia"
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				status = http.StatusServiceUnavailable
				message = "Tidak dapat memverifikasi akun saat ini"
			}
			c.JSON(status, gin.H{
				"ok":      false,
				"message": message,
			})
			c.Abort()
			return
		}
		// The address is part of the token. Changing the e-mail revokes refresh tokens;
		// this also ends access tokens issued before the change, without waiting for them to expire.
		if !strings.EqualFold(activeUser.Email, claims.Email) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"ok":      false,
				"message": "Sesi sudah tidak berlaku. Silakan masuk kembali.",
			})
			c.Abort()
			return
		}
		// The role is read from the database, not the token: an admin who is made an
		// ordinary user loses admin access at once, not when the token expires.
		role := strings.TrimSpace(activeUser.Role)
		if role == "" {
			role = "user"
		}
		c.Set("user_role", role)
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

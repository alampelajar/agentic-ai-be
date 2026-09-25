package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/auth/credentials/idtoken"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

type GoogleLoginRequest struct {
	Credential string `json:"credential"`
}

func GoogleLogin(c *gin.Context) {
	var req GoogleLoginRequest

	// ==========================================
	// PARSE REQUEST
	// ==========================================

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Google login - request tidak valid: %v", err)

		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Request tidak valid",
		})
		return
	}

	req.Credential = strings.TrimSpace(req.Credential)

	if req.Credential == "" {
		log.Println("Google login - credential kosong")

		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Credential Google wajib diisi",
		})
		return
	}

	// ==========================================
	// GOOGLE CLIENT ID
	// ==========================================

	clientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))

	if clientID == "" {
		log.Println("Google login - GOOGLE_CLIENT_ID kosong")

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "GOOGLE_CLIENT_ID belum dikonfigurasi",
		})
		return
	}

	log.Println("Google login - GOOGLE_CLIENT_ID ditemukan")

	// ==========================================
	// VALIDASI GOOGLE ID TOKEN
	// ==========================================

	payload, err := idtoken.Validate(
		context.Background(),
		req.Credential,
		clientID,
	)

	if err != nil {
		log.Printf("Google ID token validation error: %v", err)

		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Google credential tidak valid",
		})
		return
	}

	log.Println("Google login - credential berhasil divalidasi")

	// ==========================================
	// AMBIL EMAIL
	// ==========================================

	emailValue, ok := payload.Claims["email"].(string)

	if !ok || strings.TrimSpace(emailValue) == "" {
		log.Println("Google login - email tidak ditemukan")

		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Email Google tidak ditemukan",
		})
		return
	}

	email := strings.ToLower(strings.TrimSpace(emailValue))

	// ==========================================
	// AMBIL NAMA
	// ==========================================

	name := ""

	if value, ok := payload.Claims["name"].(string); ok {
		name = strings.TrimSpace(value)
	}

	if name == "" {
		name = email
	}

	// ==========================================
	// AMBIL AVATAR
	// ==========================================

	avatar := ""

	if value, ok := payload.Claims["picture"].(string); ok {
		avatar = strings.TrimSpace(value)
	}

	log.Printf("Google login - email: %s", email)

	// ==========================================
	// CARI USER DI POSTGRESQL
	// ==========================================

	var user models.User

	err = config.DB.
		Where("email = ?", email).
		First(&user).
		Error

	if err != nil {
		// ==========================================
		// USER BELUM ADA
		// ==========================================

		log.Printf(
			"Google login - user %s belum ada, membuat akun baru",
			email,
		)

		randomPassword := make([]byte, 32)

		for i := range randomPassword {
			randomPassword[i] = byte(
				time.Now().UnixNano() >> (i % 8),
			)
		}

		hashedPassword, hashErr := bcrypt.GenerateFromPassword(
			randomPassword,
			bcrypt.DefaultCost,
		)

		if hashErr != nil {
			log.Printf(
				"Google login - gagal generate password hash: %v",
				hashErr,
			)

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal membuat akun",
			})
			return
		}

		user = models.User{
			Name:     name,
			Email:    email,
			Password: string(hashedPassword),
			Avatar:   avatar,
			Role:     "user",
		}

		if createErr := config.DB.Create(&user).Error; createErr != nil {
			log.Printf(
				"Google login - gagal membuat user: %v",
				createErr,
			)

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal menyimpan akun Google",
			})
			return
		}

		log.Printf(
			"Google login - user baru berhasil dibuat, ID: %d",
			user.ID,
		)

	} else {
		// ==========================================
		// USER SUDAH ADA
		// ==========================================

		log.Printf(
			"Google login - user ditemukan, ID: %d, role: %s",
			user.ID,
			user.Role,
		)

		updates := map[string]interface{}{
			"name": name,
		}

		if avatar != "" {
			updates["avatar"] = avatar
		}

		// Pastikan user yang role-nya kosong menjadi user.
		if strings.TrimSpace(user.Role) == "" {
			updates["role"] = "user"
			user.Role = "user"
		}

		if updateErr := config.DB.
			Model(&user).
			Updates(updates).
			Error; updateErr != nil {

			log.Printf(
				"Google login - gagal update user: %v",
				updateErr,
			)

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal memperbarui profile",
			})
			return
		}

		user.Name = name

		if avatar != "" {
			user.Avatar = avatar
		}

		if strings.TrimSpace(user.Role) == "" {
			user.Role = "user"
		}
	}

	// ==========================================
	// GENERATE ACCESS TOKEN
	// ==========================================

	accessToken, err := utils.GenerateAccessToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		log.Printf(
			"Google login - gagal membuat access token: %v",
			err,
		)

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat access token",
		})
		return
	}

	// ==========================================
	// GENERATE REFRESH TOKEN
	// ==========================================

	refreshToken, err := utils.GenerateRefreshToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		log.Printf(
			"Google login - gagal membuat refresh token: %v",
			err,
		)

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat refresh token",
		})
		return
	}

	// ==========================================
	// HASH REFRESH TOKEN
	// ==========================================

	tokenHash := sha256.Sum256([]byte(refreshToken))

	refreshTokenModel := models.RefreshToken{
		UserID:    user.ID,
		TokenHash: hex.EncodeToString(tokenHash[:]),
		ExpiredAt: time.Now().Add(7 * 24 * time.Hour),
	}

	if err := config.DB.Create(&refreshTokenModel).Error; err != nil {
		log.Printf(
			"Google login - gagal menyimpan refresh token: %v",
			err,
		)

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyimpan refresh token",
		})
		return
	}

	// ==========================================
	// SET REFRESH TOKEN COOKIE
	// ==========================================

	c.SetCookie(
		"refresh_token",
		refreshToken,
		7*24*60*60,
		"/",
		"",
		false,
		true,
	)

	// ==========================================
	// RESPONSE
	// ==========================================

	log.Printf(
		"Google login berhasil - user ID: %d, email: %s, role: %s",
		user.ID,
		user.Email,
		user.Role,
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"message":      "Login Google berhasil",
		"access_token": accessToken,
		"user": gin.H{
			"id":     user.ID,
			"name":   user.Name,
			"email":  user.Email,
			"avatar": user.Avatar,
			"role":   user.Role,
		},
	})
}

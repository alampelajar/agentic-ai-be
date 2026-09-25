package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func Register(c *gin.Context) {
	var req RegisterRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Format JSON tidak valid",
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Nama wajib diisi",
		})
		return
	}

	if req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Email wajib diisi",
		})
		return
	}

	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Password minimal 8 karakter",
		})
		return
	}

	var existingUser models.User

	result := config.DB.
		Where("email = ?", req.Email).
		First(&existingUser)

	if result.Error == nil {
		c.JSON(http.StatusConflict, gin.H{
			"ok":      false,
			"message": "Email sudah terdaftar",
		})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword(
		[]byte(req.Password),
		bcrypt.DefaultCost,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengenkripsi password",
		})
		return
	}

	user := models.User{
		Name:     req.Name,
		Email:    req.Email,
		Password: string(hashedPassword),
		Role:     "user",
	}

	if err := config.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat pengguna",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"ok":      true,
		"message": "Registrasi berhasil",
		"user": gin.H{
			"id":     user.ID,
			"name":   user.Name,
			"email":  user.Email,
			"avatar": user.Avatar,
			"role":   user.Role,
		},
	})
}

func Login(c *gin.Context) {
	var req LoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Format JSON tidak valid",
		})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Email dan password wajib diisi",
		})
		return
	}

	var user models.User

	result := config.DB.
		Where("email = ?", req.Email).
		First(&user)

	if result.Error != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Email atau password salah",
		})
		return
	}

	err := bcrypt.CompareHashAndPassword(
		[]byte(user.Password),
		[]byte(req.Password),
	)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Email atau password salah",
		})
		return
	}

	if user.Role == "" {
		user.Role = "user"

		if err := config.DB.
			Model(&user).
			Update("role", "user").
			Error; err != nil {

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal memperbarui role pengguna",
			})
			return
		}
	}

	accessToken, err := utils.GenerateAccessToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat access token",
		})
		return
	}

	refreshToken, err := utils.GenerateRefreshToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat refresh token",
		})
		return
	}

	refreshTokenModel := models.RefreshToken{
		UserID:    user.ID,
		TokenHash: hashToken(refreshToken),
		ExpiredAt: time.Now().Add(7 * 24 * time.Hour),
	}

	if err := config.DB.Create(&refreshTokenModel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyimpan refresh token",
		})
		return
	}

	c.SetCookie(
		"refresh_token",
		refreshToken,
		7*24*60*60,
		"/",
		"",
		false,
		true,
	)

	c.JSON(http.StatusOK, gin.H{
	"ok":           true,
	"message":      "Login berhasil",
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

// ============================================================
// ADMIN LOGIN
// ============================================================

func AdminLogin(c *gin.Context) {
	var req LoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Format JSON tidak valid",
		})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Email dan password wajib diisi",
		})
		return
	}

	var user models.User

	result := config.DB.
		Where("email = ?", req.Email).
		First(&user)

	if result.Error != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Email atau password salah",
		})
		return
	}

	if user.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{
			"ok":      false,
			"message": "Akun ini bukan akun admin",
		})
		return
	}

	err := bcrypt.CompareHashAndPassword(
		[]byte(user.Password),
		[]byte(req.Password),
	)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Email atau password salah",
		})
		return
	}

	accessToken, err := utils.GenerateAccessToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat access token",
		})
		return
	}

	refreshToken, err := utils.GenerateRefreshToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat refresh token",
		})
		return
	}

	refreshTokenModel := models.RefreshToken{
		UserID:    user.ID,
		TokenHash: hashToken(refreshToken),
		ExpiredAt: time.Now().Add(7 * 24 * time.Hour),
	}

	if err := config.DB.Create(&refreshTokenModel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyimpan refresh token",
		})
		return
	}

	c.SetCookie(
		"refresh_token",
		refreshToken,
		7*24*60*60,
		"/",
		"",
		false,
		true,
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"message":      "Login admin berhasil",
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

func Refresh(c *gin.Context) {
	refreshToken, err := c.Cookie("refresh_token")

	if err != nil || refreshToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token tidak ditemukan",
		})
		return
	}

	claims, err := utils.ParseRefreshToken(refreshToken)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token tidak valid atau sudah expired",
		})
		return
	}

	var storedToken models.RefreshToken

	result := config.DB.
		Where(
			"token_hash = ? AND user_id = ?",
			hashToken(refreshToken),
			claims.UserID,
		).
		First(&storedToken)

	if result.Error != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token tidak ditemukan",
		})
		return
	}

	if storedToken.RevokedAt != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token sudah tidak berlaku",
		})
		return
	}

	if time.Now().After(storedToken.ExpiredAt) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token sudah expired",
		})
		return
	}

	var user models.User

	if err := config.DB.First(&user, claims.UserID).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Pengguna tidak ditemukan",
		})
		return
	}

	if user.Role == "" {
		user.Role = "user"

		if err := config.DB.
			Model(&user).
			Update("role", "user").
			Error; err != nil {

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal memperbarui role pengguna",
			})
			return
		}
	}

	newAccessToken, err := utils.GenerateAccessToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat access token",
		})
		return
	}

	newRefreshToken, err := utils.GenerateRefreshToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat refresh token",
		})
		return
	}

	now := time.Now()
	storedToken.RevokedAt = &now

	if err := config.DB.Save(&storedToken).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal memperbarui refresh token",
		})
		return
	}

	newStoredToken := models.RefreshToken{
		UserID:    user.ID,
		TokenHash: hashToken(newRefreshToken),
		ExpiredAt: time.Now().Add(7 * 24 * time.Hour),
	}

	if err := config.DB.Create(&newStoredToken).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyimpan refresh token baru",
		})
		return
	}

	c.SetCookie(
		"refresh_token",
		newRefreshToken,
		7*24*60*60,
		"/",
		"",
		false,
		true,
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"message":      "Token berhasil diperbarui",
		"access_token": newAccessToken,
	})
}

func Logout(c *gin.Context) {
	refreshToken, err := c.Cookie("refresh_token")

	if err == nil && refreshToken != "" {
		var storedToken models.RefreshToken

		result := config.DB.
			Where(
				"token_hash = ?",
				hashToken(refreshToken),
			).
			First(&storedToken)

		if result.Error == nil &&
			storedToken.RevokedAt == nil {

			now := time.Now()
			storedToken.RevokedAt = &now

			config.DB.Save(&storedToken)
		}
	}

	c.SetCookie(
		"refresh_token",
		"",
		-1,
		"/",
		"",
		false,
		true,
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Logout berhasil",
	})
}
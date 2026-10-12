package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

// ChangePassword updates the authenticated user's password after verifying
// the current password and revokes refresh sessions on other devices.
func ChangePassword(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Sesi pengguna tidak valid"})
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Format permintaan tidak valid"})
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" || req.ConfirmPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Semua kolom password wajib diisi"})
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Konfirmasi password baru tidak cocok"})
		return
	}
	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Password baru minimal 8 karakter"})
		return
	}
	// bcrypt only accepts passwords up to 72 bytes.
	if len([]byte(req.NewPassword)) > 72 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Password baru maksimal 72 byte"})
		return
	}
	if req.CurrentPassword == req.NewPassword {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Password baru harus berbeda dari password sekarang"})
		return
	}

	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Pengguna tidak ditemukan"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.CurrentPassword)); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "message": "Password sekarang tidak sesuai"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal mengenkripsi password baru"})
		return
	}

	// Keep the refresh session used by this browser, while invalidating other
	// sessions so devices signed in before the password change must log in again.
	currentRefreshToken, _ := c.Cookie("refresh_token")
	currentTokenHash := ""
	if strings.TrimSpace(currentRefreshToken) != "" {
		currentTokenHash = hashToken(currentRefreshToken)
	}

	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", user.ID).
			Update("password", string(hashedPassword)).Error; err != nil {
			return err
		}

		query := tx.Model(&models.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", user.ID)
		if currentTokenHash != "" {
			query = query.Where("token_hash <> ?", currentTokenHash)
		}
		now := time.Now()
		return query.Update("revoked_at", now).Error
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal mengubah password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Password berhasil diubah. Sesi di perangkat lain telah dicabut.",
	})
}

package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

func Me(c *gin.Context) {
	userID, exists := c.Get("user_id")

	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "User tidak ditemukan",
		})
		return
	}

	var user models.User

	if err := config.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":      false,
			"message": "Pengguna tidak ditemukan",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"user": gin.H{
			"id":     user.ID,
			"name":   user.Name,
			"email":  user.Email,
			"avatar": user.Avatar,
		},
	})
}


type UpdateMeRequest struct {
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

func validProfileAvatar(value string) bool {
	if !strings.HasPrefix(value, "preset:") {
		return false
	}

	switch strings.TrimPrefix(value, "preset:") {
	case "adventurer", "adventurer-neutral", "avataaars", "avataaars-neutral",
		"big-ears", "big-ears-neutral", "big-smile", "bottts", "bottts-neutral",
		"croodles", "croodles-neutral", "fun-emoji", "identicon", "initials",
		"lorelei", "lorelei-neutral", "micah", "miniavs", "personas",
		"pixel-art", "pixel-art-neutral", "shapes", "thumbs", "icons":
		return true
	default:
		return false
	}
}

// UpdateMe updates the authenticated user's display name and allowed avatar preset.
func UpdateMe(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok": false,
			"message": "User tidak ditemukan",
		})
		return
	}

	var req UpdateMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"message": "Format JSON tidak valid",
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if length := len([]rune(req.Name)); length < 2 || length > 30 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"message": "Nama harus terdiri dari 2 sampai 30 karakter",
		})
		return
	}
	req.Avatar = strings.TrimSpace(req.Avatar)

	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok": false,
			"message": "Pengguna tidak ditemukan",
		})
		return
	}

	// Existing provider photos remain valid if unchanged. New avatar values
	// must be one of the server-side preset identifiers.
	if req.Avatar != "" && req.Avatar != user.Avatar && !validProfileAvatar(req.Avatar) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"message": "Preset avatar tidak valid",
		})
		return
	}

	updates := map[string]interface{}{"name": req.Name}
	if req.Avatar != "" {
		updates["avatar"] = req.Avatar
	}
	if err := config.DB.Model(&user).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false,
			"message": "Gagal menyimpan profil",
		})
		return
	}

	user.Name = req.Name
	if req.Avatar != "" {
		user.Avatar = req.Avatar
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"message": "Profil berhasil diperbarui",
		"user": gin.H{
			"id": user.ID,
			"name": user.Name,
			"email": user.Email,
			"avatar": user.Avatar,
			"role": user.Role,
		},
	})
}

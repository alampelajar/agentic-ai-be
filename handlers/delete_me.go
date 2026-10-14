package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

var errCannotDeleteLastAdmin = errors.New("cannot delete the last admin account")

type DeleteAccountRequest struct {
	Password string `json:"password"`
}

// VerifyDeleteAccountPassword checks the authenticated user's password before the
// frontend displays the final confirmation dialog.
func VerifyDeleteAccountPassword(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Sesi pengguna tidak valid"})
		return
	}

	var req DeleteAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Kata sandi wajib diisi"})
		return
	}

	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Pengguna tidak ditemukan"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal memverifikasi akun"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "message": "Kata sandi tidak sesuai"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Kata sandi berhasil diverifikasi"})
}

// DeleteMe permanently deletes only the authenticated user's account and private
// resources. Shared system providers, models, and agents are preserved.
func DeleteMe(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Sesi pengguna tidak valid"})
		return
	}

	var req DeleteAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Kata sandi wajib diisi"})
		return
	}

	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Pengguna tidak ditemukan"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal mengambil data akun"})
		return
	}

	// Re-check the password when the destructive request is executed.
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "message": "Kata sandi tidak sesuai"})
		return
	}

	audit := newAuditLog(
		c,
		"user.account_deleted",
		"user",
		strconv.FormatUint(uint64(user.ID), 10),
		user.Name,
		"Pengguna menghapus akun miliknya sendiri (email: "+user.Email+").",
	)

	err := config.DB.Transaction(func(tx *gorm.DB) error {
		// Do not allow deletion of the final administrator account.
		if user.Role == "admin" {
			var adminCount int64
			if err := tx.Model(&models.User{}).Where("role = ?", "admin").Count(&adminCount).Error; err != nil {
				return err
			}
			if adminCount <= 1 {
				return errCannotDeleteLastAdmin
			}
		}

		// Resolve private providers, including soft-deleted rows.
		var providerIDs []uint
		if err := tx.Unscoped().Model(&models.AIProvider{}).
			Where("user_id = ? AND is_system = ?", user.ID, false).
			Pluck("id", &providerIDs).Error; err != nil {
			return err
		}

		// Delete non-system models created by the user or attached to their private providers.
		modelQuery := tx.Unscoped().Model(&models.AIModel{}).Where("is_system = ?", false)
		if len(providerIDs) > 0 {
			modelQuery = modelQuery.Where("(user_id = ? OR provider_id IN ?)", user.ID, providerIDs)
		} else {
			modelQuery = modelQuery.Where("user_id = ?", user.ID)
		}
		var modelIDs []uint
		if err := modelQuery.Pluck("id", &modelIDs).Error; err != nil {
			return err
		}

		// Remove references before deleting custom models to avoid dangling IDs.
		if len(modelIDs) > 0 {
			if err := tx.Model(&models.Task{}).Where("model_id IN ?", modelIDs).
				Updates(map[string]interface{}{"model_id": nil}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.TaskMessage{}).Where("model_id IN ?", modelIDs).
				Updates(map[string]interface{}{"model_id": nil}).Error; err != nil {
				return err
			}
		}

		// Remove the user's tasks and associated conversation messages.
		var taskIDs []uint
		if err := tx.Model(&models.Task{}).Where("user_id = ?", user.ID).Pluck("id", &taskIDs).Error; err != nil {
			return err
		}
		if len(taskIDs) > 0 {
			if err := tx.Where("task_id IN ?", taskIDs).Delete(&models.TaskMessage{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", taskIDs).Delete(&models.Task{}).Error; err != nil {
				return err
			}
		}

		// Remove agent-model links first, then private models and providers.
		if len(modelIDs) > 0 {
			if err := tx.Where("ai_model_id IN ?", modelIDs).Delete(&models.AgentModel{}).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Where("id IN ?", modelIDs).Delete(&models.AIModel{}).Error; err != nil {
				return err
			}
		}
		if len(providerIDs) > 0 {
			if err := tx.Unscoped().Where("id IN ?", providerIDs).Delete(&models.AIProvider{}).Error; err != nil {
				return err
			}
		}

		if err := tx.Where("user_id = ?", user.ID).Delete(&models.RefreshToken{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&audit).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(&user).Error
	})

	if errors.Is(err, errCannotDeleteLastAdmin) {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Akun admin terakhir tidak dapat dihapus"})
		return
	}
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal menghapus akun. Tidak ada perubahan yang disimpan."})
		return
	}

	c.SetCookie("refresh_token", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Akun dan data pribadi terkait berhasil dihapus permanen"})
}

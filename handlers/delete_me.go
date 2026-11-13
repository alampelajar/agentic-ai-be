package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// DeleteAccountRequest confirms account deletion with either the account password or,
// for accounts that signed in with Google and never had a password, a code e-mailed
// to the account address (see RequestDeleteAccountOTP).
type DeleteAccountRequest struct {
	Password string `json:"password"`
	OTP      string `json:"otp"`
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

// RequestDeleteAccountOTP e-mails a code to the signed-in user's own address. It is the
// way to confirm deleting an account that has no known password (Google sign-in).
func RequestDeleteAccountOTP(c *gin.Context) {
	userID := c.GetUint("user_id")
	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "message": "Sesi pengguna tidak valid"})
		return
	}

	empty := ""
	if !deliverOTP(c, models.OTPPurposeDeleteAccount, strings.ToLower(user.Email), &userID, &empty) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"message":      "Kode verifikasi dikirim ke email akun Anda.",
		"resend_after": int(services.OTPResendGap.Seconds()),
	})
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
	if err := c.ShouldBindJSON(&req); err != nil || (req.Password == "" && strings.TrimSpace(req.OTP) == "") {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Kata sandi atau kode verifikasi wajib diisi"})
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

	// Re-check the confirmation when the destructive request is executed.
	if req.Password != "" {
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"ok": false, "message": "Kata sandi tidak sesuai"})
			return
		}
	} else if !confirmDeleteWithOTP(c, &user, req.OTP) {
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

	workspaceRefs := userWorkspaceRefs(user.ID)

	err := config.DB.Transaction(func(tx *gorm.DB) error {
		return purgeUserAccount(tx, &user, &audit)
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

	removeWorkspaceFiles(user.ID, workspaceRefs)

	setRefreshCookie(c, userRefresh, "", -1)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Akun dan data pribadi terkait berhasil dihapus permanen"})
}

// confirmDeleteWithOTP checks the e-mailed code. It writes the error response itself and
// reports whether deletion may go ahead.
func confirmDeleteWithOTP(c *gin.Context, user *models.User, code string) bool {
	pepper := services.OTPPepper()
	if pepper == "" {
		otpUnavailable(c)
		return false
	}

	outcome := services.OTPInvalid
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		challenge, result, err := services.VerifyOTP(tx, models.OTPPurposeDeleteAccount, strings.ToLower(user.Email), code, pepper)
		if err != nil {
			return err
		}
		outcome = result
		// The code must belong to this user. Commit either way so a wrong guess is counted.
		if result == services.OTPVerified && (challenge.UserID == nil || *challenge.UserID != user.ID) {
			outcome = services.OTPInvalid
		}
		return nil
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Verifikasi tidak dapat diproses."})
		return false
	}
	switch outcome {
	case services.OTPVerified:
		return true
	case services.OTPTooMany:
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "code": "too_many_attempts", "message": "Batas percobaan tercapai. Minta kode baru."})
	default:
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "code": "invalid_otp", "message": "Kode verifikasi tidak valid atau kedaluwarsa."})
	}
	return false
}

// userWorkspaceRefs remembers where a user's cloud workspace files are; they are removed
// after the database part has committed.
func userWorkspaceRefs(userID uint) []string {
	var refs []string
	config.DB.Model(&models.CloudWorkspace{}).Where("user_id = ?", userID).Pluck("storage_ref", &refs)
	return refs
}

func removeWorkspaceFiles(userID uint, refs []string) {
	storage, _, err := workspaceStorage()
	if err != nil {
		return
	}
	for _, ref := range refs {
		if removeErr := storage.Delete(userID, ref); removeErr != nil {
			log.Printf("[WORKSPACE] could not remove files of a deleted account: %v", removeErr)
		}
	}
}

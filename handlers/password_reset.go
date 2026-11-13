package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// Forgot password runs on the shared e-mail OTP service (services/email_otp.go)
// with the "password_reset" purpose. After the code is verified a one-time reset
// token is issued, and only that token can set the new password.

const (
	passwordResetTokenTTL = 10 * time.Minute
	passwordResetMessage  = "Jika email terdaftar, instruksi reset password akan dikirim."
)

type requestPasswordResetPayload struct {
	Email string `json:"email" binding:"required"`
}

type verifyPasswordResetPayload struct {
	Email string `json:"email" binding:"required"`
	OTP   string `json:"otp" binding:"required"`
}

type resetPasswordWithOTPPayload struct {
	Email           string `json:"email" binding:"required"`
	ResetToken      string `json:"reset_token" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

func writeResetUnavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"ok":      false,
		"code":    "password_reset_unavailable",
		"message": "Layanan reset password belum dikonfigurasi.",
	})
}

func writeGenericResetResponse(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": passwordResetMessage,
	})
}

func generatePasswordResetToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// RequestPasswordReset sends a short-lived OTP when the supplied email belongs
// to an account. The response is deliberately generic to prevent account discovery.
func RequestPasswordReset(c *gin.Context) {
	var req requestPasswordResetPayload
	email, valid := "", false
	if err := c.ShouldBindJSON(&req); err == nil {
		email, valid = services.NormalizeEmail(req.Email)
	}
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_email", "message": "Masukkan alamat email yang valid.",
		})
		return
	}

	apiKey, sender, pepper, configured := services.EmailOTPConfig()
	if !configured {
		log.Printf("[password-reset] configuration incomplete: set RESEND_API_KEY, RESEND_FROM_EMAIL, and PASSWORD_RESET_PEPPER (or OTP_PEPPER)")
		writeResetUnavailable(c)
		return
	}

	var user models.User
	if err := config.DB.Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeGenericResetResponse(c)
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "code": "request_failed", "message": "Permintaan reset password tidak dapat diproses.",
		})
		return
	}

	empty := ""
	userID := user.ID
	otp, err := services.IssueOTP(config.DB, models.OTPPurposePasswordReset, email, &userID, &empty, pepper)
	// Same answer as for an unknown address, so the limit does not reveal accounts.
	if errors.Is(err, services.ErrOTPThrottled) || errors.Is(err, services.ErrOTPDailyLimit) {
		writeGenericResetResponse(c)
		return
	}
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "code": "request_failed", "message": "Permintaan reset password tidak dapat diproses.",
		})
		return
	}

	subject, htmlBody, textBody := services.OTPEmail(models.OTPPurposePasswordReset, otp)
	if err := services.SendEmail(c.Request.Context(), apiKey, sender, email, subject, htmlBody, textBody); err != nil {
		// Never log email addresses, OTP values, API keys, or provider response bodies.
		log.Printf("[password-reset] email delivery failed: %v", err)
		services.ClearOTP(config.DB, models.OTPPurposePasswordReset, email)
	}

	writeGenericResetResponse(c)
}

// VerifyPasswordResetOTP consumes a correct OTP once and returns a random,
// short-lived token required by the final password-reset request.
func VerifyPasswordResetOTP(c *gin.Context) {
	var req verifyPasswordResetPayload
	email, valid := "", false
	if err := c.ShouldBindJSON(&req); err == nil {
		email, valid = services.NormalizeEmail(req.Email)
	}
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_otp", "message": "Kode OTP tidak valid atau kedaluwarsa.",
		})
		return
	}

	pepper := services.OTPPepper()
	if pepper == "" {
		writeResetUnavailable(c)
		return
	}

	state := services.OTPInvalid
	resetToken := ""
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		_, result, err := services.VerifyOTP(tx, models.OTPPurposePasswordReset, email, req.OTP, pepper)
		if err != nil {
			return err
		}
		state = result
		if result != services.OTPVerified {
			return nil // commit so the attempt counter is kept
		}

		resetToken, err = generatePasswordResetToken()
		if err != nil {
			return err
		}
		return tx.Model(&models.OTPChallenge{}).
			Where("purpose = ? AND email = ?", models.OTPPurposePasswordReset, email).
			Updates(map[string]any{
				"verified_token_hash":       hashToken(resetToken),
				"verified_token_expires_at": time.Now().Add(passwordResetTokenTTL),
			}).Error
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "code": "verify_failed", "message": "Verifikasi OTP gagal diproses.",
		})
		return
	}

	if state == services.OTPTooMany {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"ok": false, "code": "too_many_attempts", "message": "Batas percobaan tercapai. Minta kode OTP baru.",
		})
		return
	}
	if state != services.OTPVerified {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_otp", "message": "Kode OTP tidak valid atau kedaluwarsa.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true, "message": "Email berhasil diverifikasi.", "reset_token": resetToken,
	})
}

// ResetPasswordWithOTP accepts the one-time reset token issued after OTP
// verification, updates the password hash, revokes all refresh sessions, and
// records an audit event.
func ResetPasswordWithOTP(c *gin.Context) {
	var req resetPasswordWithOTPPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_reset_request", "message": "Permintaan reset password tidak valid.",
		})
		return
	}

	email, valid := services.NormalizeEmail(req.Email)
	if !valid || len(req.ResetToken) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_reset_token", "message": "Sesi reset password tidak valid atau kedaluwarsa.",
		})
		return
	}
	if _, err := hex.DecodeString(req.ResetToken); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_reset_token", "message": "Sesi reset password tidak valid atau kedaluwarsa.",
		})
		return
	}
	if code, message := services.PasswordProblem(req.NewPassword, email); code != "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "code": code, "message": message})
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "password_mismatch", "message": "Konfirmasi password tidak cocok.",
		})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "code": "reset_failed", "message": "Password baru tidak dapat disimpan.",
		})
		return
	}

	state := "invalid"
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		var challenge models.OTPChallenge
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("purpose = ? AND email = ?", models.OTPPurposePasswordReset, email).
			First(&challenge)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		if result.Error != nil {
			return result.Error
		}

		now := time.Now()
		if challenge.VerifiedTokenHash == "" ||
			!hmac.Equal([]byte(challenge.VerifiedTokenHash), []byte(hashToken(req.ResetToken))) ||
			challenge.VerifiedTokenExpiresAt == nil ||
			!now.Before(*challenge.VerifiedTokenExpiresAt) {
			return nil
		}

		var user models.User
		if err := tx.Where("email = ?", email).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		if err := tx.Model(&user).Update("password", string(hashedPassword)).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", user.ID).
			Update("revoked_at", now).Error; err != nil {
			return err
		}

		userID := user.ID
		audit := models.AuditLog{
			ActorUserID: &userID,
			ActorName:   user.Name,
			ActorEmail:  user.Email,
			ActorRole:   user.Role,
			Action:      "user.password_reset",
			EntityType:  "user",
			EntityID:    strconv.FormatUint(uint64(user.ID), 10),
			EntityName:  user.Name,
			Message:     "Pengguna berhasil mereset kata sandi melalui verifikasi email.",
			CreatedAt:   now,
		}
		if err := tx.Create(&audit).Error; err != nil {
			return err
		}

		// The token works exactly once.
		if err := tx.Model(&challenge).Updates(map[string]any{
			"verified_token_hash":       "",
			"verified_token_expires_at": nil,
		}).Error; err != nil {
			return err
		}

		state = "reset"
		return nil
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "code": "reset_failed", "message": "Password baru tidak dapat disimpan.",
		})
		return
	}
	if state != "reset" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_reset_token", "message": "Sesi reset password tidak valid atau kedaluwarsa.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true, "message": "Password berhasil direset. Silakan masuk kembali.",
	})
}

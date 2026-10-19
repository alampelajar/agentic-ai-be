package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

const (
	passwordResetCodeTTL   = 5 * time.Minute
	passwordResetTokenTTL  = 10 * time.Minute
	passwordResetResendGap = time.Minute
	passwordResetMaxTries  = 5
	passwordResetMessage   = "Jika email terdaftar, instruksi reset password akan dikirim."
)

type requestPasswordResetPayload struct {
	Email string \`json:"email" binding:"required"\`
}

type verifyPasswordResetPayload struct {
	Email string \`json:"email" binding:"required"\`
	OTP   string \`json:"otp" binding:"required"\`
}

type resetPasswordWithOTPPayload struct {
	Email           string \`json:"email" binding:"required"\`
	ResetToken      string \`json:"reset_token" binding:"required"\`
	NewPassword     string \`json:"new_password" binding:"required"\`
	ConfirmPassword string \`json:"confirm_password" binding:"required"\`
}

func normalizeResetEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(parsed.Address, email) || email == "" {
		return "", false
	}
	return email, true
}

func passwordResetPepper() string {
	pepper := strings.TrimSpace(os.Getenv("PASSWORD_RESET_PEPPER"))
	if len(pepper) < 32 {
		return ""
	}
	return pepper
}

func resetEmailConfiguration() (apiKey, sender, pepper string, ok bool) {
	apiKey = strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	sender = strings.TrimSpace(os.Getenv("RESEND_FROM_EMAIL"))
	pepper = passwordResetPepper()
	return apiKey, sender, pepper, apiKey != "" && sender != "" && pepper != ""
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

func generatePasswordResetOTP() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func generatePasswordResetToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func hashPasswordResetOTP(email, otp, pepper string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	_, _ = mac.Write([]byte(email + ":" + otp))
	return hex.EncodeToString(mac.Sum(nil))
}

func sendPasswordResetEmail(ctx context.Context, apiKey, sender, recipient, otp string) error {
	message := struct {
		From    string   \`json:"from"\`
		To      []string \`json:"to"\`
		Subject string   \`json:"subject"\`
		HTML    string   \`json:"html"\`
		Text    string   \`json:"text"\`
	}{
		From:    sender,
		To:      []string{recipient},
		Subject: "Your Agentic AI password reset code",
		HTML: fmt.Sprintf(
			\`<div style="font-family:Arial,sans-serif;max-width:520px;margin:auto;color:#171717">
<h2 style="margin-bottom:12px">Reset your Agentic AI password</h2>
<p>Use this verification code to continue resetting your password.</p>
<p style="font-size:32px;font-weight:700;letter-spacing:8px;padding:16px 0">%s</p>
<p>This code expires in 5 minutes and can be used only once.</p>
<p style="color:#737373;font-size:13px">If you did not request this, you can safely ignore this email.</p>
</div>\`,
			otp,
		),
		Text: fmt.Sprintf(
			"Your Agentic AI password reset code is %s. It expires in 5 minutes and can be used only once. If you did not request this, ignore this email.",
			otp,
		),
	}

	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal Resend payload: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.resend.com/emails",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return fmt.Errorf("create Resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send Resend request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Resend returned HTTP status %d", response.StatusCode)
	}

	return nil
}

// RequestPasswordReset sends a short-lived OTP when the supplied email belongs
// to an account. The response is deliberately generic to prevent account discovery.
func RequestPasswordReset(c *gin.Context) {
	var req requestPasswordResetPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_email", "message": "Masukkan alamat email yang valid.",
		})
		return
	}

	email, valid := normalizeResetEmail(req.Email)
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_email", "message": "Masukkan alamat email yang valid.",
		})
		return
	}

	apiKey, sender, pepper, configured := resetEmailConfiguration()
	if !configured {
		log.Printf("[password-reset] configuration incomplete: set RESEND_API_KEY, RESEND_FROM_EMAIL, and PASSWORD_RESET_PEPPER")
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

	otp, err := generatePasswordResetOTP()
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "code": "request_failed", "message": "Permintaan reset password tidak dapat diproses.",
		})
		return
	}

	now := time.Now()
	rateLimited := false
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		// Serialize requests for the same address in PostgreSQL, including the
		// first request where a challenge row does not exist yet.
		if err := tx.Exec(
			"SELECT pg_advisory_xact_lock(hashtext(?))",
			"agentic-ai:password-reset:"+email,
		).Error; err != nil {
			return err
		}

		var challenge models.PasswordResetChallenge
		result := tx.Where("email = ?", email).First(&challenge)
		found := result.Error == nil
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}

		if found && now.Sub(challenge.LastSentAt) < passwordResetResendGap {
			rateLimited = true
			return nil
		}

		challenge.Email = email
		challenge.OTPHash = hashPasswordResetOTP(email, otp, pepper)
		challenge.OTPExpiresAt = now.Add(passwordResetCodeTTL)
		challenge.Attempts = 0
		challenge.LastSentAt = now
		challenge.ResetTokenHash = ""
		challenge.ResetTokenExpiresAt = nil

		if found {
			return tx.Save(&challenge).Error
		}
		return tx.Create(&challenge).Error
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "code": "request_failed", "message": "Permintaan reset password tidak dapat diproses.",
		})
		return
	}
	if rateLimited {
		writeGenericResetResponse(c)
		return
	}

	if err := sendPasswordResetEmail(c.Request.Context(), apiKey, sender, email, otp); err != nil {
		// Never log email addresses, OTP values, API keys, or provider response bodies.
		log.Printf("[password-reset] Resend email delivery failed: %v", err)
		_ = config.DB.Model(&models.PasswordResetChallenge{}).
			Where("email = ?", email).
			Updates(map[string]interface{}{
				"otp_hash":       "",
				"otp_expires_at": time.Now(),
			}).Error
	}

	writeGenericResetResponse(c)
}

// VerifyPasswordResetOTP consumes a correct OTP once and returns a random,
// short-lived token required by the final password-reset request.
func VerifyPasswordResetOTP(c *gin.Context) {
	var req verifyPasswordResetPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_otp", "message": "Kode OTP tidak valid atau kedaluwarsa.",
		})
		return
	}

	email, valid := normalizeResetEmail(req.Email)
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_otp", "message": "Kode OTP tidak valid atau kedaluwarsa.",
		})
		return
	}

	pepper := passwordResetPepper()
	if pepper == "" {
		writeResetUnavailable(c)
		return
	}

	state := "invalid"
	resetToken := ""
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		var challenge models.PasswordResetChallenge
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("email = ?", email).
			First(&challenge)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			state = "invalid"
			return nil
		}
		if result.Error != nil {
			return result.Error
		}

		now := time.Now()
		if challenge.Attempts >= passwordResetMaxTries {
			state = "too_many_attempts"
			return nil
		}
		if challenge.OTPHash == "" || !now.Before(challenge.OTPExpiresAt) {
			state = "invalid"
			return nil
		}

		expected := hashPasswordResetOTP(email, req.OTP, pepper)
		if !hmac.Equal([]byte(challenge.OTPHash), []byte(expected)) {
			challenge.Attempts++
			if err := tx.Save(&challenge).Error; err != nil {
				return err
			}
			if challenge.Attempts >= passwordResetMaxTries {
				state = "too_many_attempts"
			} else {
				state = "invalid"
			}
			return nil
		}

		var err error
		resetToken, err = generatePasswordResetToken()
		if err != nil {
			return err
		}
		tokenExpiry := now.Add(passwordResetTokenTTL)
		challenge.OTPHash = ""
		challenge.OTPExpiresAt = now
		challenge.Attempts = 0
		challenge.ResetTokenHash = hashToken(resetToken)
		challenge.ResetTokenExpiresAt = &tokenExpiry
		if err := tx.Save(&challenge).Error; err != nil {
			return err
		}
		state = "verified"
		return nil
	})
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "code": "verify_failed", "message": "Verifikasi OTP gagal diproses.",
		})
		return
	}

	if state == "too_many_attempts" {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"ok": false, "code": "too_many_attempts", "message": "Batas percobaan tercapai. Minta kode OTP baru.",
		})
		return
	}
	if state != "verified" {
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
// records the completed security event in the audit log.
func ResetPasswordWithOTP(c *gin.Context) {
	var req resetPasswordWithOTPPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "invalid_reset_request", "message": "Permintaan reset password tidak valid.",
		})
		return
	}

	email, valid := normalizeResetEmail(req.Email)
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
	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "password_too_short", "message": "Password baru minimal 8 karakter.",
		})
		return
	}
	if len([]byte(req.NewPassword)) > 72 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false, "code": "password_too_long", "message": "Password baru maksimal 72 byte.",
		})
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
		var challenge models.PasswordResetChallenge
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("email = ?", email).
			First(&challenge)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			state = "invalid"
			return nil
		}
		if result.Error != nil {
			return result.Error
		}

		now := time.Now()
		expectedTokenHash := hashToken(req.ResetToken)
		if challenge.ResetTokenHash == "" ||
			!hmac.Equal([]byte(challenge.ResetTokenHash), []byte(expectedTokenHash)) ||
			challenge.ResetTokenExpiresAt == nil ||
			!now.Before(*challenge.ResetTokenExpiresAt) {
			state = "invalid"
			return nil
		}

		var user models.User
		if err := tx.Where("email = ?", email).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				state = "invalid"
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

		challenge.ResetTokenHash = ""
		challenge.ResetTokenExpiresAt = nil
		challenge.OTPHash = ""
		challenge.OTPExpiresAt = now
		challenge.Attempts = 0
		if err := tx.Save(&challenge).Error; err != nil {
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

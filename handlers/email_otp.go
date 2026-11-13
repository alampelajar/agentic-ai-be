package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// Registration and e-mail change are confirmed with a six-digit code sent by
// e-mail. Nothing is created or changed until the code is verified.

type registerOTPRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type otpVerifyRequest struct {
	Email string `json:"email" binding:"required"`
	OTP   string `json:"otp" binding:"required"`
}

type otpResendRequest struct {
	Email string `json:"email" binding:"required"`
}

// pendingRegistration is what waits in the challenge until the code is entered.
type pendingRegistration struct {
	Name         string `json:"name"`
	PasswordHash string `json:"password_hash"`
}

func otpFail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"ok": false, "code": code, "message": message})
}

func otpUnavailable(c *gin.Context) {
	otpFail(c, http.StatusServiceUnavailable, "otp_unavailable", "Layanan verifikasi email belum dikonfigurasi.")
}

// deliverOTP issues a code and e-mails it. It writes the error response itself
// and reports whether the caller should carry on.
func deliverOTP(c *gin.Context, purpose, email string, userID *uint, payload *string) bool {
	apiKey, sender, pepper, ok := services.EmailOTPConfig()
	if !ok {
		otpUnavailable(c)
		return false
	}

	code, err := services.IssueOTP(config.DB, purpose, email, userID, payload, pepper)
	switch {
	case errors.Is(err, services.ErrOTPThrottled):
		otpFail(c, http.StatusTooManyRequests, "otp_throttled", "Tunggu sebentar sebelum meminta kode baru.")
		return false
	case errors.Is(err, services.ErrOTPDailyLimit):
		otpFail(c, http.StatusTooManyRequests, "otp_daily_limit", "Terlalu banyak kode dikirim ke email ini. Coba lagi besok.")
		return false
	case errors.Is(err, services.ErrOTPNoPending):
		otpFail(c, http.StatusBadRequest, "no_pending_verification", "Tidak ada verifikasi yang menunggu. Mulai lagi dari awal.")
		return false
	case err != nil:
		c.Error(err)
		otpFail(c, http.StatusInternalServerError, "otp_failed", "Kode verifikasi tidak dapat dibuat.")
		return false
	}

	subject, htmlBody, textBody := services.OTPEmail(purpose, code)
	if err := services.SendEmail(c.Request.Context(), apiKey, sender, email, subject, htmlBody, textBody); err != nil {
		// Never log addresses, codes, API keys or provider response bodies.
		log.Printf("[email-otp] %s delivery failed: %v", purpose, err)
		services.ClearOTP(config.DB, purpose, email)
		otpFail(c, http.StatusBadGateway, "email_send_failed", "Email verifikasi gagal dikirim. Coba lagi nanti.")
		return false
	}
	return true
}

func otpResult(c *gin.Context, result string) {
	if result == services.OTPTooMany {
		otpFail(c, http.StatusTooManyRequests, "too_many_attempts", "Batas percobaan tercapai. Minta kode baru.")
		return
	}
	otpFail(c, http.StatusBadRequest, "invalid_otp", "Kode OTP tidak valid atau kedaluwarsa.")
}

func validNewPassword(password string) (string, string) {
	if len(password) < 8 {
		return "password_too_short", "Password minimal 8 karakter."
	}
	if len([]byte(password)) > 72 {
		return "password_too_long", "Password maksimal 72 byte."
	}
	return "", ""
}

// RequestRegisterOTP validates the sign-up form and e-mails a code. The account
// is created only by VerifyRegisterOTP.
func RequestRegisterOTP(c *gin.Context) {
	var req registerOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		otpFail(c, http.StatusBadRequest, "invalid_request", "Format JSON tidak valid.")
		return
	}

	name := strings.TrimSpace(req.Name)
	email, valid := services.NormalizeEmail(req.Email)
	if name == "" {
		otpFail(c, http.StatusBadRequest, "name_required", "Nama wajib diisi.")
		return
	}
	if !valid {
		otpFail(c, http.StatusBadRequest, "invalid_email", "Email tidak valid.")
		return
	}
	if code, message := validNewPassword(req.Password); code != "" {
		otpFail(c, http.StatusBadRequest, code, message)
		return
	}

	var taken int64
	if err := config.DB.Model(&models.User{}).Where("email = ?", email).Count(&taken).Error; err != nil {
		c.Error(err)
		otpFail(c, http.StatusInternalServerError, "otp_failed", "Pendaftaran tidak dapat diproses.")
		return
	}
	if taken > 0 {
		otpFail(c, http.StatusConflict, "email_taken", "Email sudah terdaftar.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.Error(err)
		otpFail(c, http.StatusInternalServerError, "otp_failed", "Pendaftaran tidak dapat diproses.")
		return
	}
	encoded, _ := json.Marshal(pendingRegistration{Name: name, PasswordHash: string(hash)})
	payload := string(encoded)

	if !deliverOTP(c, models.OTPPurposeRegister, email, nil, &payload) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Kode verifikasi dikirim ke email Anda.", "resend_after": int(services.OTPResendGap.Seconds())})
}

// ResendRegisterOTP sends a new code for a sign-up that is waiting.
func ResendRegisterOTP(c *gin.Context) {
	var req otpResendRequest
	email, valid := "", false
	if err := c.ShouldBindJSON(&req); err == nil {
		email, valid = services.NormalizeEmail(req.Email)
	}
	if !valid {
		otpFail(c, http.StatusBadRequest, "invalid_email", "Email tidak valid.")
		return
	}
	if !deliverOTP(c, models.OTPPurposeRegister, email, nil, nil) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Kode verifikasi dikirim ulang.", "resend_after": int(services.OTPResendGap.Seconds())})
}

// VerifyRegisterOTP creates the account once the code is right.
func VerifyRegisterOTP(c *gin.Context) {
	var req otpVerifyRequest
	email, valid := "", false
	if err := c.ShouldBindJSON(&req); err == nil {
		email, valid = services.NormalizeEmail(req.Email)
	}
	if !valid {
		otpResult(c, services.OTPInvalid)
		return
	}
	pepper := services.OTPPepper()
	if pepper == "" {
		otpUnavailable(c)
		return
	}

	var (
		outcome = services.OTPInvalid
		created models.User
		taken   bool
	)
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		challenge, result, err := services.VerifyOTP(tx, models.OTPPurposeRegister, email, req.OTP, pepper)
		if err != nil {
			return err
		}
		outcome = result
		if result != services.OTPVerified {
			return nil // commit so the attempt counter is kept
		}

		var pending pendingRegistration
		if err := json.Unmarshal([]byte(challenge.Payload), &pending); err != nil || pending.Name == "" || pending.PasswordHash == "" {
			outcome = services.OTPInvalid
			return nil
		}

		var count int64
		if err := tx.Model(&models.User{}).Where("email = ?", email).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			taken = true
			return nil
		}

		created = models.User{Name: pending.Name, Email: email, Password: pending.PasswordHash, Role: "user"}
		return tx.Create(&created).Error
	})
	if err != nil {
		c.Error(err)
		otpFail(c, http.StatusInternalServerError, "verify_failed", "Verifikasi tidak dapat diproses.")
		return
	}
	if taken {
		otpFail(c, http.StatusConflict, "email_taken", "Email sudah terdaftar.")
		return
	}
	if outcome != services.OTPVerified {
		otpResult(c, outcome)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"ok":      true,
		"message": "Registrasi berhasil",
		"user": gin.H{
			"id": created.ID, "name": created.Name, "email": created.Email, "avatar": created.Avatar, "role": created.Role,
		},
	})
}

type changeEmailRequest struct {
	NewEmail string `json:"new_email" binding:"required"`
	Password string `json:"password"`
}

type changeEmailVerifyRequest struct {
	NewEmail string `json:"new_email" binding:"required"`
	OTP      string `json:"otp" binding:"required"`
}

// RequestChangeEmailOTP e-mails a code to the NEW address. The current password
// is required, so a hijacked session cannot take the account over.
func RequestChangeEmailOTP(c *gin.Context) {
	var req changeEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		otpFail(c, http.StatusBadRequest, "invalid_request", "Format JSON tidak valid.")
		return
	}
	newEmail, valid := services.NormalizeEmail(req.NewEmail)
	if !valid {
		otpFail(c, http.StatusBadRequest, "invalid_email", "Email tidak valid.")
		return
	}

	userID := c.GetUint("user_id")
	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
		otpFail(c, http.StatusUnauthorized, "unauthorized", "Sesi tidak valid.")
		return
	}
	if user.Password == "" || bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)) != nil {
		otpFail(c, http.StatusForbidden, "wrong_password", "Password salah.")
		return
	}
	if newEmail == strings.ToLower(user.Email) {
		otpFail(c, http.StatusBadRequest, "same_email", "Email baru sama dengan email saat ini.")
		return
	}

	var taken int64
	if err := config.DB.Model(&models.User{}).Where("email = ?", newEmail).Count(&taken).Error; err != nil {
		c.Error(err)
		otpFail(c, http.StatusInternalServerError, "otp_failed", "Permintaan tidak dapat diproses.")
		return
	}
	if taken > 0 {
		otpFail(c, http.StatusConflict, "email_taken", "Email sudah terdaftar.")
		return
	}

	payload := strconv.FormatUint(uint64(userID), 10)
	if !deliverOTP(c, models.OTPPurposeChangeEmail, newEmail, &userID, &payload) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Kode verifikasi dikirim ke email baru.", "resend_after": int(services.OTPResendGap.Seconds())})
}

// ResendChangeEmailOTP sends a new code for a change that is waiting.
func ResendChangeEmailOTP(c *gin.Context) {
	var req struct {
		NewEmail string `json:"new_email" binding:"required"`
	}
	newEmail, valid := "", false
	if err := c.ShouldBindJSON(&req); err == nil {
		newEmail, valid = services.NormalizeEmail(req.NewEmail)
	}
	if !valid {
		otpFail(c, http.StatusBadRequest, "invalid_email", "Email tidak valid.")
		return
	}
	userID := c.GetUint("user_id")
	var pending models.OTPChallenge
	if err := config.DB.Where("purpose = ? AND email = ? AND user_id = ?", models.OTPPurposeChangeEmail, newEmail, userID).First(&pending).Error; err != nil {
		otpFail(c, http.StatusBadRequest, "no_pending_verification", "Tidak ada verifikasi yang menunggu. Mulai lagi dari awal.")
		return
	}
	if !deliverOTP(c, models.OTPPurposeChangeEmail, newEmail, &userID, nil) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Kode verifikasi dikirim ulang.", "resend_after": int(services.OTPResendGap.Seconds())})
}

// VerifyChangeEmailOTP swaps the account's address once the new mailbox is
// proven. Every session is signed out, and the old address is told.
func VerifyChangeEmailOTP(c *gin.Context) {
	var req changeEmailVerifyRequest
	newEmail, valid := "", false
	if err := c.ShouldBindJSON(&req); err == nil {
		newEmail, valid = services.NormalizeEmail(req.NewEmail)
	}
	if !valid {
		otpResult(c, services.OTPInvalid)
		return
	}
	pepper := services.OTPPepper()
	if pepper == "" {
		otpUnavailable(c)
		return
	}

	userID := c.GetUint("user_id")
	var (
		outcome  = services.OTPInvalid
		oldEmail string
		taken    bool
	)
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		challenge, result, err := services.VerifyOTP(tx, models.OTPPurposeChangeEmail, newEmail, req.OTP, pepper)
		if err != nil {
			return err
		}
		outcome = result
		if result != services.OTPVerified {
			return nil
		}
		// The code must belong to the signed-in user who asked for it.
		if challenge.UserID == nil || *challenge.UserID != userID {
			outcome = services.OTPInvalid
			return nil
		}

		var count int64
		if err := tx.Model(&models.User{}).Where("email = ?", newEmail).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			taken = true
			return nil
		}

		var user models.User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		oldEmail = user.Email
		now := time.Now()
		if err := tx.Model(&user).Update("email", newEmail).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", user.ID).
			Update("revoked_at", now).Error; err != nil {
			return err
		}
		audit := models.AuditLog{
			ActorUserID: &userID,
			ActorName:   user.Name,
			ActorEmail:  newEmail,
			ActorRole:   user.Role,
			Action:      "user.email_changed",
			EntityType:  "user",
			EntityID:    strconv.FormatUint(uint64(user.ID), 10),
			EntityName:  user.Name,
			Message:     "Pengguna mengganti email melalui verifikasi OTP.",
			CreatedAt:   now,
		}
		return tx.Create(&audit).Error
	})
	if err != nil {
		c.Error(err)
		otpFail(c, http.StatusInternalServerError, "verify_failed", "Verifikasi tidak dapat diproses.")
		return
	}
	if taken {
		otpFail(c, http.StatusConflict, "email_taken", "Email sudah terdaftar.")
		return
	}
	if outcome != services.OTPVerified {
		otpResult(c, outcome)
		return
	}

	// Best effort: tell the old address so a takeover does not go unnoticed.
	if apiKey, sender, _, ok := services.EmailOTPConfig(); ok && oldEmail != "" {
		text := "The email on your Agentic AI account was changed. If this was not you, contact support immediately."
		htmlBody := "<p>" + text + "</p>"
		if err := services.SendEmail(c.Request.Context(), apiKey, sender, oldEmail, "Your Agentic AI email was changed", htmlBody, text); err != nil {
			log.Printf("[email-otp] change_email notice failed: %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Email berhasil diganti. Silakan masuk kembali.",
		"email":   newEmail,
	})
}

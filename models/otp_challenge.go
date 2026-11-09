package models

import "time"

// OTP purposes. One challenge row exists per (purpose, email).
const (
	OTPPurposeRegister      = "register"
	OTPPurposeChangeEmail   = "change_email"
	OTPPurposePasswordReset = "password_reset"
)

// OTPChallenge stores a short-lived email verification code. The code is kept
// only as an HMAC hash. Payload carries data held back until the code is
// confirmed (for register: the name and the bcrypt password hash).
type OTPChallenge struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	Purpose      string    `json:"-" gorm:"size:32;not null;uniqueIndex:idx_otp_purpose_email"`
	Email        string    `json:"-" gorm:"size:255;not null;uniqueIndex:idx_otp_purpose_email"`
	UserID       *uint     `json:"-" gorm:"index"`
	OTPHash      string    `json:"-" gorm:"size:64;not null;default:''"`
	OTPExpiresAt time.Time `json:"-" gorm:"not null"`
	Attempts     int       `json:"-" gorm:"not null;default:0"`
	LastSentAt   time.Time `json:"-" gorm:"not null;index"`
	Payload      string    `json:"-" gorm:"type:text;not null;default:''"`
	// One-time token issued after the code is verified (password reset only).
	VerifiedTokenHash      string     `json:"-" gorm:"size:64;not null;default:''"`
	VerifiedTokenExpiresAt *time.Time `json:"-"`
	CreatedAt              time.Time  `json:"-"`
	UpdatedAt              time.Time  `json:"-"`
}

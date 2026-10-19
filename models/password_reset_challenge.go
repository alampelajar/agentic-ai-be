package models

import "time"

// PasswordResetChallenge stores short-lived password-reset verification data.
// OTPs and reset tokens are stored only as hashes, never as plaintext.
type PasswordResetChallenge struct {
	ID                   uint       `json:"id" gorm:"primaryKey"`
	Email                string     `json:"-" gorm:"size:255;not null;uniqueIndex"`
	OTPHash              string     `json:"-" gorm:"size:64;not null;default:''`
	OTPExpiresAt         time.Time  `json:"-" gorm:"not null"`
	Attempts             int        `json:"-" gorm:"not null;default:0"`
	LastSentAt           time.Time  `json:"-" gorm:"not null;index"`
	ResetTokenHash       string     `json:"-" gorm:"size:64;not null;default:''"`
	ResetTokenExpiresAt  *time.Time `json:"-" gorm:"index"`
	CreatedAt            time.Time  `json:"-"`
	UpdatedAt            time.Time  `json:"-"`
}

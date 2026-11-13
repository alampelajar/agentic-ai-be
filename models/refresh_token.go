package models

import "time"

type RefreshToken struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	UserID    uint       `json:"user_id" gorm:"not null;index"`
	TokenHash string     `json:"-" gorm:"not null;uniqueIndex"`
	ExpiredAt time.Time  `json:"expired_at" gorm:"not null"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at"`
	// ReplacedAt is set when the token was revoked because a refresh rotated it.
	// Only such a token gets the short grace period for concurrent refreshes; one
	// revoked by sign-out, a password change or a detected reuse never does.
	ReplacedAt *time.Time `json:"-"`

	User User `json:"-" gorm:"foreignKey:UserID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

package models

import "time"

// EmailSignup records that an account was opened with an address, so an address
// cannot open account after account (delete one, make a new one, again). It keeps no
// readable address: KeyHash is emailpolicy.SignupKey, a keyed hash of the canonical
// address. It outlives the account (UserID is cleared when the account is purged, and
// has no foreign key for that reason).
type EmailSignup struct {
	ID        uint      `gorm:"primaryKey"`
	KeyHash   string    `gorm:"size:64;not null;index"`
	UserID    *uint     `gorm:"index"`
	CreatedAt time.Time `gorm:"index"`
}

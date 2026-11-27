package models

import (
	"time"

	"gorm.io/gorm"

	"agentic-ai-backend/internal/emailpolicy"
)

type User struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Name  string `json:"name" gorm:"not null"`
	Email string `json:"email" gorm:"uniqueIndex;not null"`
	// CanonicalEmail is Email in the form that names the mailbox (emailpolicy.Canonical):
	// namif+1@gmail.com and na.mif@gmail.com are both namif@gmail.com. A new account may
	// not share it with another, so one mailbox cannot open several accounts.
	CanonicalEmail string `json:"-" gorm:"index;not null;default:''"`
	Password       string `json:"-"`
	Avatar         string `json:"avatar"`
	Role           string `json:"role" gorm:"type:varchar(20);not null;default:user"`
	// IsPremium lifts the Website Builder limits (number of websites, monthly tokens) and
	// lets the builder use every active system model instead of the default one only.
	IsPremium bool `json:"is_premium" gorm:"not null;default:false"`
	// PremiumUntil ends the premium account at that moment; nil means it does not end.
	PremiumUntil *time.Time `json:"premium_until"`
	// RestoredAt is when an admin gave the account back after its owner deleted it. A
	// restore happens once: deleting the account again removes it at once.
	RestoredAt *time.Time `json:"-"`
	// SuspendedAt is when an admin suspended the account, for example after it tried
	// to make a forbidden website; nil when it is not suspended. A suspended account
	// cannot sign in, every session ends, and its published websites go offline.
	// SuspendReason is what the person is told.
	SuspendedAt   *time.Time `json:"suspended_at"`
	SuspendReason string     `json:"suspend_reason" gorm:"type:varchar(300);not null;default:''"`
	// The e-mails the person wants (Settings, Notifications): one when a website or a
	// change is finished, and one when a visitor sends a message through a website.
	NotifyWebsiteReady bool `json:"notify_website_ready" gorm:"not null;default:true"`
	NotifyNewMessages  bool `json:"notify_new_messages" gorm:"not null;default:true"`
	// Credits pay for the Website Builder: a new website uses 2, a change uses 1. A new
	// account starts with 10 (also the default for accounts made before credits); more
	// come from an admin (a top-up). An account in BUILDER_UNLIMITED_EMAILS uses none.
	Credits int `json:"credits" gorm:"not null;default:10"`
	// New-account promo: 10 free credits and 50% off top-ups for 30 days. An account made
	// from the same device as another, or from an address many accounts came from, gets
	// neither (PromoEligible false, PromoNote says why); an admin may change it.
	PromoEligible bool   `json:"-" gorm:"not null;default:true"`
	PromoNote     string `json:"-" gorm:"type:varchar(160)"`
	// Keyed hashes of the sign-up address and the browser's random device id, never the
	// values themselves: they can be compared, not read back.
	SignupIP     string         `json:"-" gorm:"type:varchar(64);index"`
	SignupDevice string         `json:"-" gorm:"type:varchar(64);index"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// BeforeCreate fills CanonicalEmail. Code that changes Email on an existing account
// sets it too (see CanonicalEmailUpdates).
func (u *User) BeforeCreate(*gorm.DB) error {
	u.CanonicalEmail = emailpolicy.Canonical(u.Email)
	return nil
}

// CanonicalEmailUpdates is the column set for changing an account's address.
func CanonicalEmailUpdates(email string) map[string]any {
	return map[string]any{"email": email, "canonical_email": emailpolicy.Canonical(email)}
}

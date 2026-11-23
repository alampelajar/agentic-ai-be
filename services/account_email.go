package services

import (
	"time"

	"gorm.io/gorm"

	"agentic-ai-backend/internal/emailpolicy"
	"agentic-ai-backend/models"
)

const (
	// MaxAccountsPerAddress is how many accounts one mailbox may open within
	// SignupHistoryWindow. Deleting an account and opening a new one with the same
	// address (to start the free quota again, say) counts each time.
	MaxAccountsPerAddress = 2
	SignupHistoryWindow   = 365 * 24 * time.Hour
)

// The answers when an address may not be used; the app translates the code.
const (
	EmailDomainCode    = "email_domain_not_allowed"
	EmailDomainMessage = "Gunakan email dari penyedia umum, seperti Gmail, Yahoo, Outlook, atau iCloud."
	EmailTakenCode     = "email_taken"
	EmailTakenMessage  = "Email sudah terdaftar."
	EmailReusedCode    = "email_reused"
	EmailReusedMessage = "Email ini sudah terlalu sering dipakai untuk membuat akun. Gunakan email lain."
)

// EmailRefusal says why an address may not be used. A zero value means it may.
type EmailRefusal struct {
	Code    string
	Message string
}

// Refused reports whether the address was refused.
func (r EmailRefusal) Refused() bool { return r.Code != "" }

// CheckEmailAddress checks the address alone, without the database: not a throwaway
// service, and a well-known provider (or one the operator allows). Handlers call it
// first, before anything else is looked at.
func CheckEmailAddress(email string) EmailRefusal {
	if IsDisposableEmail(email) {
		return EmailRefusal{DisposableEmailCode, DisposableEmailMessage}
	}
	if !emailpolicy.DomainAllowed(email) {
		return EmailRefusal{EmailDomainCode, EmailDomainMessage}
	}
	return EmailRefusal{}
}

// emailUsable checks the address itself and whether another account already has its
// mailbox (an alias such as name+1@gmail.com counts as the same mailbox).
func emailUsable(tx *gorm.DB, email string, exceptUserID uint) (EmailRefusal, error) {
	if refusal := CheckEmailAddress(email); refusal.Refused() {
		return refusal, nil
	}
	var taken int64
	query := tx.Unscoped().Model(&models.User{}).Where("canonical_email = ?", emailpolicy.Canonical(email))
	if exceptUserID != 0 {
		query = query.Where("id <> ?", exceptUserID)
	}
	if err := query.Count(&taken).Error; err != nil {
		return EmailRefusal{}, err
	}
	if taken > 0 {
		return EmailRefusal{EmailTakenCode, EmailTakenMessage}, nil
	}
	return EmailRefusal{}, nil
}

// CheckNewAccountEmail decides whether a new account may be opened with the address:
// not a throwaway service, a well-known provider (or one the operator allows), no
// other account on the same mailbox, and not too many accounts opened with it already.
func CheckNewAccountEmail(tx *gorm.DB, email string) (EmailRefusal, error) {
	refusal, err := emailUsable(tx, email, 0)
	if err != nil || refusal.Refused() {
		return refusal, err
	}
	var opened int64
	if err := tx.Model(&models.EmailSignup{}).
		Where("key_hash = ? AND created_at > ?", emailpolicy.SignupKey(email, emailpolicy.Secret()), time.Now().Add(-SignupHistoryWindow)).
		Count(&opened).Error; err != nil {
		return EmailRefusal{}, err
	}
	if opened >= MaxAccountsPerAddress {
		return EmailRefusal{EmailReusedCode, EmailReusedMessage}, nil
	}
	return EmailRefusal{}, nil
}

// CheckEmailChange decides whether an existing account may move to the address. The
// account count does not apply: no account is opened.
func CheckEmailChange(tx *gorm.DB, email string, userID uint) (EmailRefusal, error) {
	return emailUsable(tx, email, userID)
}

// RecordSignup notes that an account was opened with the address.
func RecordSignup(tx *gorm.DB, email string, userID uint) error {
	id := userID
	return tx.Create(&models.EmailSignup{KeyHash: emailpolicy.SignupKey(email, emailpolicy.Secret()), UserID: &id}).Error
}

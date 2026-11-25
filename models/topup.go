package models

import "time"

// The states of a top-up request: sent with a proof of payment, then approved
// (the credits are added) or rejected (with a reason) by an admin.
const (
	TopUpPending  = "pending"
	TopUpApproved = "approved"
	TopUpRejected = "rejected"
)

// CreditReasonTopUp is a top-up an admin approved.
const CreditReasonTopUp = "topup"

// TopUpSettings is how people pay for credits, set by an admin: one row. Top-up is
// offered only while it is enabled and has a price, packages and payment details.
type TopUpSettings struct {
	ID      uint `json:"-" gorm:"primaryKey"`
	Enabled bool `json:"enabled" gorm:"not null;default:false"`
	// PricePerCredit is in Rupiah.
	PricePerCredit int `json:"price_per_credit" gorm:"not null;default:0"`
	// Packages are the numbers of credits a person may buy, comma separated ("10,25,50").
	Packages string `json:"packages" gorm:"type:varchar(200);not null;default:''"`
	// PaymentInfo is where to pay, one line each ("BCA 1234567890 a.n. ...").
	PaymentInfo string    `json:"payment_info" gorm:"type:text;not null;default:''"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TopUpRequest is a person's request for credits, with the screenshot of their
// payment. The price is the one shown when they sent it.
type TopUpRequest struct {
	ID      uint `json:"id" gorm:"primaryKey"`
	UserID  uint `json:"user_id" gorm:"not null;index"`
	Credits int  `json:"credits" gorm:"not null"`
	Price   int  `json:"price" gorm:"not null"`
	// DiscountPercent is the new-account discount taken off the price, 0 for none.
	DiscountPercent int    `json:"discount_percent" gorm:"not null;default:0"`
	Status          string `json:"status" gorm:"type:varchar(20);not null;index"`
	// ProofPath is the stored screenshot (uploads/topups/<name>); never public.
	ProofPath  string     `json:"-" gorm:"type:varchar(200);not null"`
	AdminNote  string     `json:"admin_note" gorm:"type:varchar(300)"`
	ReviewedBy *uint      `json:"-"`
	ReviewedAt *time.Time `json:"reviewed_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

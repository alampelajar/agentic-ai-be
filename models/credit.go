package models

import "time"

// The reasons a person's credits change.
const (
	CreditReasonCreate = "create" // a new website was made
	CreditReasonRevise = "revise" // a website was changed (a request, another look, photos)
	CreditReasonAdmin  = "admin"  // an admin added or took away credits (a top-up)
)

// CreditEntry is one change of a person's credits: what was used for a website, or
// what an admin added. Credits are used only when the work succeeded, once per run
// (AgentRunID is unique), so a run that is continued or repeated is never charged twice.
type CreditEntry struct {
	ID     uint `json:"id" gorm:"primaryKey"`
	UserID uint `json:"-" gorm:"not null;index"`
	// Amount is negative when credits were used, positive when they were added.
	Amount int `json:"amount" gorm:"not null"`
	// Balance is the person's credits right after this change.
	Balance    int       `json:"balance" gorm:"not null"`
	Reason     string    `json:"reason" gorm:"type:varchar(20);not null"`
	SiteID     *uint     `json:"site_id"`
	AgentRunID *uint     `json:"-" gorm:"uniqueIndex"`
	AdminID    *uint     `json:"-"`
	Note       string    `json:"note" gorm:"type:varchar(200)"`
	CreatedAt  time.Time `json:"created_at"`
}

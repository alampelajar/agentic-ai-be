package models

import "time"

type Feedback struct {
	ID            uint             `json:"id" gorm:"primaryKey"`
	UserID        uint             `json:"user_id" gorm:"not null;index"`
	Category      string           `json:"category" gorm:"size:40;not null"`
	Subject       string           `json:"subject" gorm:"size:120"`
	Description   string           `json:"description" gorm:"type:text;not null"`
	RelatedPage   string           `json:"related_page" gorm:"size:255;not null"`
	Attachment    string           `json:"attachment"`
	Status        string           `json:"status" gorm:"size:20;not null;default:new;index"`
	Rating        int              `json:"rating" gorm:"not null;default:0"`
	Replies       []FeedbackReply  `json:"replies,omitempty" gorm:"foreignKey:FeedbackID;constraint:OnDelete:CASCADE"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type FeedbackReply struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	FeedbackID uint      `json:"feedback_id" gorm:"not null;index"`
	AdminID    uint      `json:"admin_id" gorm:"not null"`
	Message    string    `json:"message" gorm:"type:text;not null"`
	CreatedAt  time.Time `json:"created_at"`
}

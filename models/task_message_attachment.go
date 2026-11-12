package models

import "time"

// TaskMessageAttachment is a file a person attached to a chat message, kept so the conversation
// still shows it after a reload. The file itself lives on disk under StorageKey.
type TaskMessageAttachment struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	TaskMessageID uint      `json:"task_message_id" gorm:"not null;index"`
	UserID        uint      `json:"-" gorm:"not null;index"`
	Name          string    `json:"name" gorm:"type:varchar(255);not null"`
	MimeType      string    `json:"mime_type" gorm:"type:varchar(100);not null"`
	Size          int64     `json:"size" gorm:"not null"`
	IsImage       bool      `json:"is_image" gorm:"not null;default:false"`
	StorageKey    string    `json:"-" gorm:"type:varchar(255);not null"`
	CreatedAt     time.Time `json:"created_at"`
	// Deleted with its message: the cascade is declared on TaskMessage.Attachments.
}

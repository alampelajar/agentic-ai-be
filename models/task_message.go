package models

import "time"

type TaskMessage struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	TaskID    uint      `json:"task_id" gorm:"not null;index"`
	Role      string    `json:"role" gorm:"type:varchar(20);not null"`
	Content   string    `json:"content" gorm:"type:text;not null"`
	Model     string    `json:"model,omitempty" gorm:"type:varchar(255)"`
	ModelID   *uint     `json:"model_id,omitempty" gorm:"index"`
	Provider  string    `json:"provider,omitempty" gorm:"type:varchar(255)"`
	CreatedAt time.Time `json:"created_at"`

	Task Task `json:"-" gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE"`
}

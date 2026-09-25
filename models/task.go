package models

import "time"

type Task struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	UserID      uint      `json:"user_id" gorm:"not null;index"`
	AgentID     *uint     `json:"agent_id,omitempty" gorm:"index"`
	ModelID     *uint     `json:"model_id,omitempty" gorm:"index"`
	Title       string    `json:"title" gorm:"type:varchar(255);not null"`
	Description string    `json:"description" gorm:"type:text"`
	Status      string    `json:"status" gorm:"type:varchar(30);not null;default:todo"`
	Label       string    `json:"label" gorm:"type:varchar(50);not null;default:feature"`
	Priority    string    `json:"priority" gorm:"type:varchar(30);not null;default:medium"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	User     User          `json:"-" gorm:"foreignKey:UserID"`
	Agent    *Agent        `json:"agent,omitempty" gorm:"foreignKey:AgentID"`
	Messages []TaskMessage `json:"messages,omitempty" gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE"`
}

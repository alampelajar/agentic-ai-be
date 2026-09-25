package models

import "time"

type AgentModel struct {
	ID uint `json:"id" gorm:"primaryKey"`

	AgentID uint `json:"agent_id" gorm:"not null;index;uniqueIndex:idx_agent_model"`

	AIModelID uint `json:"ai_model_id" gorm:"not null;index;uniqueIndex:idx_agent_model"`

	IsDefault bool `json:"is_default" gorm:"not null;default:false"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Agent   Agent   `json:"-" gorm:"foreignKey:AgentID"`
	AIModel AIModel `json:"-" gorm:"foreignKey:AIModelID"`
}

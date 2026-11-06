package models

import "time"

// Combo strategies. A combo is a model made of several models.
const (
	ComboStrategyFallback   = "fallback"
	ComboStrategyRoundRobin = "round_robin"
)

// AIModelCombo is always owned by a user: only users build combos. Each combo has a
// virtual row in ai_models (AIModelID, on the internal "combo" provider) so it appears
// in the model lists and can be linked to agents like any other model.
type AIModelCombo struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	UserID      uint   `json:"user_id" gorm:"not null;index;uniqueIndex:idx_combo_user_slug"`
	AIModelID   uint   `json:"ai_model_id" gorm:"not null;uniqueIndex"`
	Name        string `json:"name" gorm:"type:varchar(100);not null"`
	Slug        string `json:"slug" gorm:"type:varchar(100);not null;uniqueIndex:idx_combo_user_slug"`
	Description string `json:"description" gorm:"type:text"`
	Strategy    string `json:"strategy" gorm:"type:varchar(30);not null;default:fallback"`
	// MaxAttempts caps how many members one request may try.
	MaxAttempts int       `json:"max_attempts" gorm:"not null;default:3"`
	IsActive    bool      `json:"is_active" gorm:"not null;default:true"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	Members []AIModelComboMember `json:"members,omitempty" gorm:"foreignKey:ComboID;constraint:OnDelete:CASCADE"`
}

type AIModelComboMember struct {
	ID        uint `json:"id" gorm:"primaryKey"`
	ComboID   uint `json:"combo_id" gorm:"not null;index;uniqueIndex:idx_combo_member"`
	AIModelID uint `json:"ai_model_id" gorm:"not null;index;uniqueIndex:idx_combo_member"`
	// Position is the order for fallback, and the order after the chosen model for round-robin.
	Position  int       `json:"position" gorm:"not null;default:0"`
	Weight    int       `json:"weight" gorm:"not null;default:1"`
	IsEnabled bool      `json:"is_enabled" gorm:"not null;default:true"`
	CreatedAt time.Time `json:"created_at"`

	AIModel AIModel `json:"ai_model,omitempty" gorm:"foreignKey:AIModelID"`
}

// AIModelCallLog records every attempt a combo makes, which is also what the daily
// allowance for the system's shared models is counted from.
type AIModelCallLog struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	UserID     uint      `json:"user_id" gorm:"not null;index:idx_call_log_user_day"`
	ComboID    uint      `json:"combo_id" gorm:"index"`
	AIModelID  uint      `json:"ai_model_id" gorm:"not null;index"`
	IsSystem   bool      `json:"is_system" gorm:"not null;default:false;index:idx_call_log_user_day"`
	Attempt    int       `json:"attempt" gorm:"not null"`
	Status     string    `json:"status" gorm:"type:varchar(20);not null"`
	ErrorClass string    `json:"error_class,omitempty" gorm:"type:varchar(20)"`
	LatencyMS  int64     `json:"latency_ms"`
	CreatedAt  time.Time `json:"created_at" gorm:"index:idx_call_log_user_day"`
}

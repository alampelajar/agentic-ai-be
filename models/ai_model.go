package models

import (
	"time"

	"gorm.io/gorm"
)

type AIModel struct {
	ID uint `json:"id" gorm:"primaryKey"`

	ProviderID uint `json:"provider_id" gorm:"not null;index"`

	// NULL untuk model sistem.
	UserID *uint `json:"user_id,omitempty" gorm:"index"`

	Name string `json:"name" gorm:"type:varchar(150);not null"`

	// ID asli yang dikirim ke provider.
	// Contoh:
	// agnes-2.5-flash
	// gpt-5.6
	// gemini-2.5-flash
	ModelID string `json:"model_id" gorm:"type:varchar(200);not null"`

	Description string `json:"description" gorm:"type:text"`

	IsSystem bool `json:"is_system" gorm:"not null;default:false"`

	IsActive bool `json:"is_active" gorm:"not null;default:true"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	Provider AIProvider `json:"provider,omitempty" gorm:"foreignKey:ProviderID"`

	Agents []Agent `json:"agents,omitempty" gorm:"many2many:agent_models;"`
}

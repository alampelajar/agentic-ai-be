package models

import (
	"time"

	"gorm.io/gorm"
)

type AIProvider struct {
	ID uint `json:"id" gorm:"primaryKey"`

	UserID *uint `json:"user_id,omitempty" gorm:"index"`

	Name string `json:"name" gorm:"type:varchar(100);not null"`

	Slug string `json:"slug" gorm:"type:varchar(100);not null"`

	BaseURL string `json:"base_url" gorm:"type:text;not null"`

	APIKeyEncrypted string `json:"-" gorm:"type:text"`

	Type string `json:"type" gorm:"type:varchar(50);not null;default:custom"`

	IsSystem bool `json:"is_system" gorm:"not null;default:false"`

	IsActive bool `json:"is_active" gorm:"not null;default:true"`

	CreatedAt time.Time `json:"created_at"`

	UpdatedAt time.Time `json:"updated_at"`

	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	User *User `json:"-" gorm:"foreignKey:UserID"`

	Models []AIModel `json:"models,omitempty" gorm:"foreignKey:ProviderID"`
}

// ============================================================
// ACTUAL DATABASE TABLE NAME
// ============================================================

func (AIProvider) TableName() string {
	return "a_iproviders"
}
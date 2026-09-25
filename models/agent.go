package models

import "time"

type Agent struct {
	ID uint `json:"id" gorm:"primaryKey"`

	Slug string `json:"slug" gorm:"type:varchar(50);uniqueIndex;not null"`

	Name string `json:"name" gorm:"type:varchar(100);not null"`

	Description string `json:"description" gorm:"type:text"`

	Status string `json:"status" gorm:"type:varchar(30);not null;default:ready"`

	IsActive bool `json:"is_active" gorm:"not null;default:true"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Models []AIModel `json:"models,omitempty" gorm:"many2many:agent_models;"`
}

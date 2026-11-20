package models

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	Name      string         `json:"name" gorm:"not null"`
	Email     string         `json:"email" gorm:"uniqueIndex;not null"`
	Password  string         `json:"-"`
	Avatar    string         `json:"avatar"`
	Role      string         `json:"role" gorm:"type:varchar(20);not null;default:user"`
	// IsPremium lifts the Website Builder limits (number of websites, monthly tokens) and
	// lets the builder use every active system model instead of the default one only.
	IsPremium bool           `json:"is_premium" gorm:"not null;default:false"`
	// PremiumUntil ends the premium account at that moment; nil means it does not end.
	PremiumUntil *time.Time `json:"premium_until"`
	// RestoredAt is when an admin gave the account back after its owner deleted it. A
	// restore happens once: deleting the account again removes it at once.
	RestoredAt *time.Time `json:"-"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

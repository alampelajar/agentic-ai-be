package models

import "time"

// AuditLog records security-relevant actions. Actor identity and target details
// are stored as snapshots so the entry survives account deletion.
type AuditLog struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	ActorUserID *uint     `json:"actor_user_id,omitempty" gorm:"index"`
	ActorName   string    `json:"actor_name" gorm:"type:varchar(150);not null;default:''"`
	ActorEmail  string    `json:"actor_email" gorm:"type:varchar(255);not null;default:'';index"`
	ActorRole   string    `json:"actor_role" gorm:"type:varchar(20);not null;default:''"`
	Action      string    `json:"action" gorm:"type:varchar(80);not null;index"`
	EntityType  string    `json:"entity_type" gorm:"type:varchar(50);not null;index"`
	EntityID    string    `json:"entity_id" gorm:"type:varchar(100);not null;default:'';index"`
	EntityName  string    `json:"entity_name" gorm:"type:varchar(255);not null;default:''"`
	Message     string    `json:"message" gorm:"type:text;not null;default:''"`
	CreatedAt   time.Time `json:"created_at" gorm:"index"`
}

package models

import "time"

const (
	DelegationStatusCompleted = "completed"
	DelegationStatusFailed    = "failed"
)

// AgentDelegation records one piece of work a user handed from one of their
// agents to another. Instruction and result are stored after credential
// redaction and are limited in size.
type AgentDelegation struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	UserID      uint      `json:"user_id" gorm:"not null;index"`
	TaskID      *uint     `json:"task_id" gorm:"index"`
	FromAgentID *uint     `json:"from_agent_id"`
	ToAgentID   uint      `json:"to_agent_id" gorm:"not null;index"`
	Instruction string    `json:"instruction" gorm:"type:text;not null"`
	Result      string    `json:"result" gorm:"type:text;not null;default:''"`
	Status      string    `json:"status" gorm:"type:varchar(20);not null"`
	Error       string    `json:"error" gorm:"type:text;not null;default:''"`
	DurationMS  int64     `json:"duration_ms" gorm:"not null;default:0"`
	CreatedAt   time.Time `json:"created_at"`
}

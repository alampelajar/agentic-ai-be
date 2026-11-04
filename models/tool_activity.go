package models

import "time"

type ToolActivity struct {
	ID                uint      `json:"id" gorm:"primaryKey"`
	UserID            uint      `json:"user_id" gorm:"index;not null"`
	AgentID           *uint     `json:"agent_id,omitempty" gorm:"index"`
	SkillID           *uint     `json:"skill_id,omitempty" gorm:"index"`
	ToolName          string    `json:"tool_name" gorm:"type:varchar(100);not null;index"`
	ArgumentsJSON     string    `json:"arguments_json" gorm:"type:jsonb;not null"`
	TargetApplication string    `json:"target_application" gorm:"type:varchar(255);not null;default:''"`
	ApprovalStatus    string    `json:"approval_status" gorm:"type:varchar(30);not null"`
	ExecutionStatus   string    `json:"execution_status" gorm:"type:varchar(30);not null"`
	Error             string    `json:"error,omitempty" gorm:"type:text"`
	DurationMS        int64     `json:"duration_ms"`
	CreatedAt         time.Time `json:"created_at" gorm:"index"`
}

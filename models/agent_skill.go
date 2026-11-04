package models

import "time"

// AgentSkill is deliberately scoped by user. Agents are currently system-level
// records, but each user's assignment must remain private and independent.
type AgentSkill struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"not null;uniqueIndex:idx_user_agent_skill"`
	AgentID   uint      `json:"agent_id" gorm:"not null;uniqueIndex:idx_user_agent_skill"`
	SkillID   uint      `json:"skill_id" gorm:"not null;uniqueIndex:idx_user_agent_skill"`
	IsEnabled bool      `json:"is_enabled" gorm:"not null;default:true"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Skill Skill `json:"skill" gorm:"foreignKey:SkillID;constraint:OnDelete:CASCADE"`
}

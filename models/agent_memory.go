package models

import "time"

// Memory categories, statuses and limits live here so the API, the retrieval
// code and the tests agree on them.
const (
	MemoryStatusActive   = "active"
	MemoryStatusArchived = "archived"

	MemoryMaxTitleLength    = 120
	MemoryMaxContentLength  = 2000
	MemoryMinImportance     = 1
	MemoryMaxImportance     = 5
	MemoryDefaultImportance = 3
)

var MemoryCategories = []string{"profile", "preference", "project", "technical_context", "decision", "instruction"}

// AgentMemory is one fact the user asked an agent to remember. AgentID nil means
// the memory is global: every agent of the same user may use it. A memory is
// removed with a hard delete; there is no soft-deleted copy left behind.
type AgentMemory struct {
	ID           uint       `json:"id" gorm:"primaryKey"`
	UserID       uint       `json:"user_id" gorm:"not null;index:idx_agent_memories_user_status,priority:1"`
	AgentID      *uint      `json:"agent_id" gorm:"index"`
	Category     string     `json:"category" gorm:"type:varchar(30);not null"`
	Title        string     `json:"title" gorm:"type:varchar(120);not null"`
	Content      string     `json:"content" gorm:"type:text;not null"`
	Importance   int        `json:"importance" gorm:"not null;default:3"`
	Status       string     `json:"status" gorm:"type:varchar(20);not null;default:active;index:idx_agent_memories_user_status,priority:2"`
	SourceTaskID *uint      `json:"source_task_id" gorm:"index"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

// UserMemorySetting holds the per-user switch for Persistent Memory. A user
// without a row has it off.
type UserMemorySetting struct {
	UserID    uint      `json:"-" gorm:"primaryKey"`
	Enabled   bool      `json:"enabled" gorm:"not null;default:false"`
	UpdatedAt time.Time `json:"updated_at"`
}

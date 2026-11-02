package models

import "time"

type Skill struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	UserID      uint           `json:"user_id" gorm:"not null;index"`
	Slug        string         `json:"slug" gorm:"type:varchar(100);not null;uniqueIndex:idx_skills_user_slug"`
	Name        string         `json:"name" gorm:"type:varchar(150);not null"`
	Description string         `json:"description" gorm:"type:text"`
	Author      string         `json:"author" gorm:"type:varchar(150)"`
	Source      string         `json:"source" gorm:"type:varchar(30);not null;default:created"`
	Status      string         `json:"status" gorm:"type:varchar(30);not null;default:draft;index"`
	IsEnabled   bool           `json:"is_enabled" gorm:"not null;default:false"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Versions    []SkillVersion `json:"versions,omitempty" gorm:"foreignKey:SkillID;constraint:OnDelete:CASCADE"`
}

type SkillVersion struct {
	ID                   uint      `json:"id" gorm:"primaryKey"`
	SkillID              uint      `json:"skill_id" gorm:"not null;index;uniqueIndex:idx_skill_version"`
	Version              string    `json:"version" gorm:"type:varchar(50);not null;uniqueIndex:idx_skill_version"`
	Manifest             string    `json:"manifest" gorm:"type:jsonb;not null"`
	Instructions         string    `json:"instructions" gorm:"type:text;not null"`
	RequestedTools       string    `json:"requested_tools" gorm:"type:jsonb;not null;default:'[]'"`
	RequestedPermissions string    `json:"requested_permissions" gorm:"type:jsonb;not null;default:'[]'"`
	PackageSHA256        string    `json:"package_sha256" gorm:"type:varchar(64);not null"`
	SourceURL            string    `json:"source_url,omitempty" gorm:"type:text"`
	ValidationStatus     string    `json:"validation_status" gorm:"type:varchar(30);not null;default:valid"`
	ValidationError      string    `json:"validation_error,omitempty" gorm:"type:text"`
	CreatedAt            time.Time `json:"created_at"`
}

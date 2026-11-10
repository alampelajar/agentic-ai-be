package models

import "time"

// GitHubPublication records one time a workspace's changes were sent to GitHub.
type GitHubPublication struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	UserID         uint      `json:"user_id" gorm:"not null;index"`
	WorkspaceID    uint      `json:"workspace_id" gorm:"not null;index"`
	Repository     string    `json:"repository" gorm:"type:varchar(200);not null"` // owner/repo
	Branch         string    `json:"branch" gorm:"type:varchar(255);not null"`
	BaseBranch     string    `json:"base_branch" gorm:"type:varchar(255);not null;default:''"`
	CommitSHA      string    `json:"commit_sha" gorm:"type:varchar(40);not null"`
	CommitURL      string    `json:"commit_url" gorm:"type:varchar(500);not null;default:''"`
	PullRequestURL string    `json:"pull_request_url" gorm:"type:varchar(500);not null;default:''"`
	FileCount      int       `json:"file_count" gorm:"not null;default:0"`
	CreatedAt      time.Time `json:"created_at"`
}

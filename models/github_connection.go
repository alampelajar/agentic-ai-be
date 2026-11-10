package models

import "time"

// GitHubConnection links a user to their GitHub account. The access token is kept
// encrypted (utils.EncryptString) and is never sent to the browser or written to a log.
type GitHubConnection struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	UserID      uint      `json:"user_id" gorm:"not null;uniqueIndex"`
	GitHubID    int64     `json:"github_id" gorm:"not null"`
	GitHubLogin string    `json:"github_login" gorm:"type:varchar(100);not null"`
	TokenEnc    string    `json:"-" gorm:"type:text;not null"`
	Scope       string    `json:"scope" gorm:"type:varchar(200);not null;default:''"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GitHubOAuthState is one pending "connect GitHub" attempt. Only the hash of the random state
// is stored; the attempt is single use and short lived, which stops a forged callback.
type GitHubOAuthState struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	StateHash string    `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	UserID    uint      `json:"user_id" gorm:"not null;index"`
	ExpiresAt time.Time `json:"expires_at" gorm:"not null;index"`
	CreatedAt time.Time `json:"created_at"`
}

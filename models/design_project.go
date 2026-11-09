package models

import "time"

const (
	DesignProjectMaxNameLength  = 120
	DesignProjectMaxDocumentLen = 6 << 20
	DesignProjectMaxPerUser     = 100
)

// DesignProject is one Design Studio file. The document is the editor's own JSON
// (frames, layers, components, tokens and imported images); the server stores it
// whole and only checks that it is a JSON object of a sane size. Revision goes up
// by one on every save, so a stale tab cannot overwrite newer work.
type DesignProject struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"not null;index"`
	Name      string    `json:"name" gorm:"type:varchar(120);not null"`
	Document  string    `json:"-" gorm:"type:text;not null"`
	Revision  int       `json:"revision" gorm:"not null;default:1"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

package models

import "time"

const (
	WorkspaceSourceEmpty  = "empty"
	WorkspaceSourceZip    = "zip"
	WorkspaceSourceGitHub = "github"

	WorkspaceStatusCreating    = "creating"
	WorkspaceStatusReady       = "ready"
	WorkspaceStatusUnavailable = "unavailable"
	WorkspaceStatusDeleting    = "deleting"
	WorkspaceStatusFailed      = "failed"
)

// CloudWorkspace is a project the user keeps on the server. The files are in
// WorkspaceStorage under StorageRef; the database holds only metadata.
type CloudWorkspace struct {
	ID             uint       `json:"id" gorm:"primaryKey"`
	UserID         uint       `json:"user_id" gorm:"not null;index"`
	Name           string     `json:"name" gorm:"type:varchar(100);not null"`
	Description    string     `json:"description" gorm:"type:varchar(500);not null;default:''"`
	SourceType     string     `json:"source_type" gorm:"type:varchar(20);not null;default:empty"`
	SourceURL      string     `json:"source_url" gorm:"type:varchar(300);not null;default:''"`
	Status         string     `json:"status" gorm:"type:varchar(20);not null;default:creating;index"`
	StorageRef     string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	DefaultRuntime string     `json:"default_runtime" gorm:"type:varchar(20);not null;default:''"`
	FileCount      int        `json:"file_count" gorm:"not null;default:0"`
	SizeBytes      int64      `json:"size_bytes" gorm:"not null;default:0"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastAccessedAt *time.Time `json:"last_accessed_at"`
}

const (
	JobQueued    = "queued"
	JobPreparing = "preparing"
	JobRunning   = "running"
	JobCompleted = "completed"
	JobFailed    = "failed"
	JobTimedOut  = "timed_out"
	JobCancelled = "cancelled"
)

// ExecutionJob is one allowlisted command to run against a workspace. The API
// server only creates and reads jobs; the execution worker runs them.
// AgentRunID has no foreign key: agent runs do not exist yet.
type ExecutionJob struct {
	ID              uint       `json:"id" gorm:"primaryKey"`
	UserID          uint       `json:"user_id" gorm:"not null;index"`
	WorkspaceID     uint       `json:"workspace_id" gorm:"not null;index"`
	TaskID          *uint      `json:"task_id" gorm:"index"`
	AgentRunID      *uint      `json:"agent_run_id"`
	AgentID         *uint      `json:"agent_id"`
	JobType         string     `json:"job_type" gorm:"type:varchar(30);not null"`
	Command         string     `json:"command" gorm:"type:varchar(300);not null"`
	Runtime         string     `json:"runtime" gorm:"type:varchar(20);not null"`
	Status          string     `json:"status" gorm:"type:varchar(20);not null;default:queued;index"`
	CancelRequested bool       `json:"cancel_requested" gorm:"not null;default:false"`
	ExitCode        *int       `json:"exit_code"`
	Stdout          string     `json:"stdout" gorm:"type:text;not null;default:''"`
	Stderr          string     `json:"stderr" gorm:"type:text;not null;default:''"`
	Error           string     `json:"error" gorm:"type:text;not null;default:''"`
	TimeoutSeconds  int        `json:"timeout_seconds" gorm:"not null"`
	DurationMS      int64      `json:"duration_ms" gorm:"not null;default:0"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at"`
}

const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalDenied   = "denied"
	ApprovalExpired  = "expired"
)

// WorkspaceToolApproval is a request for a tool that changes files or runs a
// command. It is bound to the user, the workspace, the tool and a hash of the
// arguments, can be approved once, and expires.
type WorkspaceToolApproval struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(64)"`
	UserID      uint      `json:"user_id" gorm:"not null;index"`
	WorkspaceID uint      `json:"workspace_id" gorm:"not null;index"`
	AgentID     *uint     `json:"agent_id"`
	TaskID      *uint     `json:"task_id"`
	Tool        string    `json:"tool" gorm:"type:varchar(60);not null"`
	ArgsJSON    string    `json:"-" gorm:"type:text;not null"`
	ArgsHash    string    `json:"-" gorm:"type:varchar(64);not null"`
	Status      string    `json:"status" gorm:"type:varchar(20);not null;default:pending"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

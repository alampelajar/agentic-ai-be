package models

import "time"

// Website Builder: a person describes a website, the agent worker builds it in a
// workspace, the site runner shows a preview and serves published versions.
// See WEBSITE-BUILDER.md in the frontend repository.

const (
	SiteStatusDraft    = "draft"    // described, questions and brief in progress
	SiteStatusBuilding = "building" // a run is working on it
	SiteStatusReady    = "ready"    // has a version that can be previewed
	SiteStatusFailed   = "failed"   // the first run failed; nothing to preview yet
)

// Site is one website. Its files live in the workspace WorkspaceID; versions and
// published data live under SITE_DATA_ROOT (see internal/sitebuilder).
type Site struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	UserID      uint   `json:"user_id" gorm:"not null;index"`
	WorkspaceID uint   `json:"workspace_id" gorm:"not null;index"`
	Name        string `json:"name" gorm:"type:varchar(100);not null"`
	Description string `json:"description" gorm:"type:text;not null;default:''"`
	// Language is the language of the website's own text, taken from the description:
	// "id" or "en". UILanguage is the language the person reads the builder in
	// (questions, progress, messages). They differ when someone writes an Indonesian
	// description with the app in English.
	Language   string `json:"language" gorm:"type:varchar(5);not null;default:id"`
	UILanguage string `json:"ui_language" gorm:"type:varchar(5);not null;default:id"`
	// Kind is what sort of site it is (food, workshop, portfolio...), used to offer directions.
	Kind            string `json:"kind" gorm:"type:varchar(30);not null;default:''"`
	Status          string `json:"status" gorm:"type:varchar(20);not null;default:draft;index"`
	QuestionsJSON   string `json:"-" gorm:"type:text;not null;default:''"`
	AnswersJSON     string `json:"-" gorm:"type:text;not null;default:''"`
	BriefJSON       string `json:"-" gorm:"type:text;not null;default:''"`
	ImagesJSON      string `json:"-" gorm:"type:text;not null;default:''"`
	DesignDirection string `json:"design_direction" gorm:"type:varchar(40);not null;default:''"`
	// DesignVariant is the curated palette of the direction ("" is its base palette).
	DesignVariant string `json:"design_variant" gorm:"type:varchar(40);not null;default:''"`
	// Slug is the published address (<slug>.<SITES_DOMAIN>); empty until first published.
	Slug               *string    `json:"slug" gorm:"type:varchar(40);uniqueIndex"`
	CurrentVersionID   *uint      `json:"current_version_id"`
	PublishedReleaseID *uint      `json:"published_release_id"`
	PreviewRequestedAt *time.Time `json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

const (
	AgentRunCreate = "create"
	AgentRunRevise = "revise"

	RunQueued    = "queued"
	RunRunning   = "running"
	RunCompleted = "completed"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// AgentRun is one piece of work the agent worker does on a site: building it from
// the brief, or a change the person asked for. The worker runs it on the server, so it
// continues when the browser is closed.
type AgentRun struct {
	ID     uint   `json:"id" gorm:"primaryKey"`
	SiteID uint   `json:"site_id" gorm:"not null;index"`
	UserID uint   `json:"user_id" gorm:"not null;index"`
	Kind   string `json:"kind" gorm:"type:varchar(20);not null"`
	// Request is the person's own words for a revision, empty for a create run.
	Request string `json:"request" gorm:"type:text;not null;default:''"`
	// Direction is set when the person chose another look: the run rewrites the theme.
	Direction       string `json:"direction" gorm:"type:varchar(40);not null;default:''"`
	Variant         string `json:"variant" gorm:"type:varchar(40);not null;default:''"`
	Status          string `json:"status" gorm:"type:varchar(20);not null;default:queued;index"`
	CancelRequested bool   `json:"cancel_requested" gorm:"not null;default:false"`
	// Background runs (the looks' example websites) wait for every person's run and
	// run one at a time, so they never hold up someone who is waiting.
	Background   bool       `json:"-" gorm:"not null;default:false;index"`
	ErrorMessage string     `json:"error_message" gorm:"type:text;not null;default:''"`
	Summary      string     `json:"summary" gorm:"type:text;not null;default:''"`
	ModelCalls   int        `json:"model_calls" gorm:"not null;default:0"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

const (
	StepModel  = "model"
	StepBuild  = "build"
	StepReview = "review"
	StepInfo   = "info"

	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
)

// AgentRunStep is one thing the run did. Summary is the plain sentence the person
// sees; Detail (redacted and bounded) is only shown under "Advanced".
type AgentRunStep struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	AgentRunID uint      `json:"agent_run_id" gorm:"not null;index"`
	Index      int       `json:"index" gorm:"not null"`
	Kind       string    `json:"kind" gorm:"type:varchar(20);not null"`
	Summary    string    `json:"summary" gorm:"type:varchar(300);not null"`
	Detail     string    `json:"detail" gorm:"type:text;not null;default:''"`
	Status     string    `json:"status" gorm:"type:varchar(20);not null"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SiteVersion is the state of a site after a successful run. Its files (source and
// built output) are kept, unchanged, under SITE_DATA_ROOT/<site>/versions/<number>.
type SiteVersion struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	SiteID     uint      `json:"site_id" gorm:"not null;uniqueIndex:idx_site_version_number"`
	AgentRunID *uint     `json:"agent_run_id"`
	Number     int       `json:"number" gorm:"not null;uniqueIndex:idx_site_version_number"`
	Summary    string    `json:"summary" gorm:"type:text;not null;default:''"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	ReleaseLive       = "live"
	ReleaseSuperseded = "superseded"
)

// SiteRelease is one publication of a version at the site's public address.
type SiteRelease struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	SiteID        uint      `json:"site_id" gorm:"not null;index"`
	SiteVersionID uint      `json:"site_version_id" gorm:"not null"`
	Number        int       `json:"number" gorm:"not null"`
	Slug          string    `json:"slug" gorm:"type:varchar(40);not null"`
	Status        string    `json:"status" gorm:"type:varchar(20);not null"`
	PublishedAt   time.Time `json:"published_at"`
}

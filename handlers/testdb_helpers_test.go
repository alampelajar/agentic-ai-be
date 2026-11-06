package handlers

import (
	"gorm.io/gorm"

	"agentic-ai-backend/internal/testdb"
	"agentic-ai-backend/models"
)

// migrateAll creates every table the application uses, as config.ConnectDatabase
// does. The integration tests call it so that each one works on an empty test
// database, instead of relying on tables another test happened to create earlier.
func migrateAll(db *gorm.DB) error {
	return testdb.Migrate(db,
		&models.User{},
		&models.RefreshToken{},
		&models.PasswordResetChallenge{},
		&models.OTPChallenge{},
		&models.AIProvider{},
		&models.AIModel{},
		&models.Agent{},
		&models.AgentModel{},
		&models.Task{},
		&models.TaskMessage{},
		&models.AuditLog{},
		&models.Skill{},
		&models.SkillVersion{},
		&models.AgentSkill{},
		&models.ToolActivity{},
		&models.AgentMemory{},
		&models.UserMemorySetting{},
		&models.AgentDelegation{},
		&models.CloudWorkspace{},
		&models.ExecutionJob{},
		&models.WorkspaceToolApproval{},
		&models.Feedback{},
		&models.FeedbackReply{},
		&models.AIModelCombo{},
		&models.AIModelComboMember{},
		&models.AIModelCallLog{},
	)
}

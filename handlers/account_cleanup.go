package handlers

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"agentic-ai-backend/models"
)

var errCannotDeleteLastAdmin = errors.New("cannot delete the last admin account")

// purgeUserAccount removes a user and everything that belongs to them, inside tx.
// Shared system providers, models and agents are kept. It is used both when users
// delete their own account and when an administrator deletes one: deleting only the
// users row fails on the foreign keys of tasks and providers (and would leave the
// rest of the person's data behind).
//
// The caller records its own audit entry before calling, so it commits or rolls back
// together with the deletion.
func purgeUserAccount(tx *gorm.DB, user *models.User, audit *models.AuditLog) error {
	// Do not allow deletion of the final administrator account.
	if user.Role == "admin" {
		var adminCount int64
		if err := tx.Model(&models.User{}).Where("role = ?", "admin").Count(&adminCount).Error; err != nil {
			return err
		}
		if adminCount <= 1 {
			return errCannotDeleteLastAdmin
		}
	}

	// Resolve private providers, including soft-deleted rows.
	var providerIDs []uint
	if err := tx.Unscoped().Model(&models.AIProvider{}).
		Where("user_id = ? AND is_system = ?", user.ID, false).
		Pluck("id", &providerIDs).Error; err != nil {
		return err
	}

	// Delete non-system models created by the user or attached to their private providers.
	modelQuery := tx.Unscoped().Model(&models.AIModel{}).Where("is_system = ?", false)
	if len(providerIDs) > 0 {
		modelQuery = modelQuery.Where("(user_id = ? OR provider_id IN ?)", user.ID, providerIDs)
	} else {
		modelQuery = modelQuery.Where("user_id = ?", user.ID)
	}
	var modelIDs []uint
	if err := modelQuery.Pluck("id", &modelIDs).Error; err != nil {
		return err
	}

	// Remove references before deleting custom models to avoid dangling IDs.
	if len(modelIDs) > 0 {
		if err := tx.Model(&models.Task{}).Where("model_id IN ?", modelIDs).
			Updates(map[string]interface{}{"model_id": nil}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.TaskMessage{}).Where("model_id IN ?", modelIDs).
			Updates(map[string]interface{}{"model_id": nil}).Error; err != nil {
			return err
		}
	}

	// Remove the user's tasks and associated conversation messages.
	var taskIDs []uint
	if err := tx.Model(&models.Task{}).Where("user_id = ?", user.ID).Pluck("id", &taskIDs).Error; err != nil {
		return err
	}
	if len(taskIDs) > 0 {
		if err := tx.Where("user_id = ?", user.ID).Delete(&models.TaskMessageAttachment{}).Error; err != nil {
			return err
		}
		removeUserAttachmentFiles(user.ID)
		if err := tx.Where("task_id IN ?", taskIDs).Delete(&models.TaskMessage{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", taskIDs).Delete(&models.Task{}).Error; err != nil {
			return err
		}
	}

	// Model combos (their members and usage log) go before the models they point at.
	var comboIDs []uint
	if err := tx.Model(&models.AIModelCombo{}).Where("user_id = ?", user.ID).Pluck("id", &comboIDs).Error; err != nil {
		return err
	}
	if len(comboIDs) > 0 {
		if err := tx.Where("combo_id IN ?", comboIDs).Delete(&models.AIModelComboMember{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", comboIDs).Delete(&models.AIModelCombo{}).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.AIModelCallLog{}).Error; err != nil {
		return err
	}

	// The user's own agents, with their model and skill links.
	var agentIDs []uint
	if err := tx.Model(&models.Agent{}).Where("user_id = ?", user.ID).Pluck("id", &agentIDs).Error; err != nil {
		return err
	}
	if len(agentIDs) > 0 {
		if err := tx.Where("agent_id IN ?", agentIDs).Delete(&models.AgentModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_id IN ?", agentIDs).Delete(&models.AgentSkill{}).Error; err != nil {
			return err
		}
	}

	// Remove agent-model links first, then private models and providers.
	if len(modelIDs) > 0 {
		if err := tx.Where("ai_model_id IN ?", modelIDs).Delete(&models.AgentModel{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("id IN ?", modelIDs).Delete(&models.AIModel{}).Error; err != nil {
			return err
		}
	}
	if len(providerIDs) > 0 {
		if err := tx.Unscoped().Where("id IN ?", providerIDs).Delete(&models.AIProvider{}).Error; err != nil {
			return err
		}
	}
	if len(agentIDs) > 0 {
		if err := tx.Where("id IN ?", agentIDs).Delete(&models.Agent{}).Error; err != nil {
			return err
		}
	}

	// Skills the user wrote, and where they were assigned.
	var skillIDs []uint
	if err := tx.Unscoped().Model(&models.Skill{}).Where("user_id = ?", user.ID).Pluck("id", &skillIDs).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.AgentSkill{}).Error; err != nil {
		return err
	}
	if len(skillIDs) > 0 {
		if err := tx.Where("skill_id IN ?", skillIDs).Delete(&models.AgentSkill{}).Error; err != nil {
			return err
		}
		if err := tx.Where("skill_id IN ?", skillIDs).Delete(&models.SkillVersion{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("id IN ?", skillIDs).Delete(&models.Skill{}).Error; err != nil {
			return err
		}
	}

	// Feedback tickets and their replies.
	var feedbackIDs []uint
	if err := tx.Model(&models.Feedback{}).Where("user_id = ?", user.ID).Pluck("id", &feedbackIDs).Error; err != nil {
		return err
	}
	if len(feedbackIDs) > 0 {
		if err := tx.Where("feedback_id IN ?", feedbackIDs).Delete(&models.FeedbackReply{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", feedbackIDs).Delete(&models.Feedback{}).Error; err != nil {
			return err
		}
	}

	if err := tx.Where("user_id = ?", user.ID).Delete(&models.RefreshToken{}).Error; err != nil {
		return err
	}
	// The GitHub connection holds an access token to the person's repositories.
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.GitHubConnection{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.GitHubOAuthState{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.GitHubPublication{}).Error; err != nil {
		return err
	}
	// Design projects are personal data: remove them with the account.
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.DesignProject{}).Error; err != nil {
		return err
	}
	// Persistent memories are personal data: remove them with the account.
	if err := tx.Unscoped().Where("user_id = ?", user.ID).Delete(&models.AgentMemory{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.UserMemorySetting{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.AgentDelegation{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.ToolActivity{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.ExecutionJob{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&models.WorkspaceToolApproval{}).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Where("user_id = ?", user.ID).Delete(&models.CloudWorkspace{}).Error; err != nil {
		return err
	}
	// Pending e-mail codes for this address or user.
	if err := tx.Where("user_id = ? OR lower(email) = ?", user.ID, strings.ToLower(user.Email)).Delete(&models.OTPChallenge{}).Error; err != nil {
		return err
	}
	if err := tx.Create(audit).Error; err != nil {
		return err
	}
	return tx.Unscoped().Delete(user).Error
}

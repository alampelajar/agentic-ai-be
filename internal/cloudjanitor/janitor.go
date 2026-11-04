// Package cloudjanitor implements the lifecycle policy for Cloud Workspaces:
// which workspaces have been unused for so long that they may be removed.
//
// The policy is deliberately cautious. A workspace is only a candidate when it
// has not been opened for the whole retention period, is not in use (no queued,
// preparing or running job) and has a final status. Nothing is deleted unless the
// caller asks for it, and a workspace that was opened recently is never touched.
package cloudjanitor

import (
	"time"

	"gorm.io/gorm"

	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/models"
)

// MinRetention is the shortest retention that can be asked for. It stops a typo
// from turning a cleanup into a mass deletion.
const MinRetention = 7 * 24 * time.Hour

// lastUsed is when the workspace was last opened, or created if it never was.
func lastUsed(workspace models.CloudWorkspace) time.Time {
	if workspace.LastAccessedAt != nil {
		return *workspace.LastAccessedAt
	}
	return workspace.CreatedAt
}

// Inactive returns the workspaces that were last used before now minus retention.
func Inactive(db *gorm.DB, retention time.Duration, now time.Time) ([]models.CloudWorkspace, error) {
	if retention < MinRetention {
		retention = MinRetention
	}
	cutoff := now.Add(-retention)
	var candidates []models.CloudWorkspace
	err := db.Where("status = ? AND COALESCE(last_accessed_at, created_at) < ?", models.WorkspaceStatusReady, cutoff).
		Where("NOT EXISTS (SELECT 1 FROM execution_jobs WHERE execution_jobs.workspace_id = cloud_workspaces.id AND execution_jobs.status IN ?)",
			[]string{models.JobQueued, models.JobPreparing, models.JobRunning}).
		Order("COALESCE(last_accessed_at, created_at) ASC").Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	// Re-check in Go so the rule is stated once, in plain terms.
	kept := candidates[:0]
	for _, workspace := range candidates {
		if lastUsed(workspace).Before(cutoff) {
			kept = append(kept, workspace)
		}
	}
	return kept, nil
}

// Remove deletes one workspace, its files first. Tasks keep their history and lose
// only the link; the workspace's jobs and approvals go with it.
func Remove(db *gorm.DB, storage cloudworkspace.WorkspaceStorage, workspace models.CloudWorkspace) error {
	if err := storage.Delete(workspace.UserID, workspace.StorageRef); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Task{}).Where("workspace_id = ? AND user_id = ?", workspace.ID, workspace.UserID).UpdateColumn("workspace_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Where("workspace_id = ? AND user_id = ?", workspace.ID, workspace.UserID).Delete(&models.ExecutionJob{}).Error; err != nil {
			return err
		}
		if err := tx.Where("workspace_id = ? AND user_id = ?", workspace.ID, workspace.UserID).Delete(&models.WorkspaceToolApproval{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Where("id = ? AND user_id = ?", workspace.ID, workspace.UserID).Delete(&models.CloudWorkspace{}).Error
	})
}

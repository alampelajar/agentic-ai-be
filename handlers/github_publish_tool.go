package handlers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/models"
)

// publishFingerprintKey is set by the server when the approval is created; it is not in the tool's
// schema, so a model cannot supply or change it.
const publishFingerprintKey = "_changes_fingerprint"

const publishPreviewFiles = 50

func publishTarget(workspace models.CloudWorkspace, args toolArgs) (branch string, openPR bool) {
	branch, _ = args.str("branch")
	if branch == "" {
		branch = "(otomatis: agentic/...)"
	}
	choice, _ := args.str("pull_request")
	return branch, choice != "skip"
}

func publishApprovalSummary(workspace models.CloudWorkspace, args toolArgs, files []cloudworkspace.PublishFile) string {
	branch, openPR := publishTarget(workspace, args)
	text := fmt.Sprintf("Send %d changed file(s) to GitHub %s/%s on the new branch %s", len(files), workspace.GitHubOwner, workspace.GitHubRepo, branch)
	if openPR {
		text += fmt.Sprintf(", with a pull request into %s", workspace.BaseBranch)
	}
	return text
}

// publishApprovalPreview is what the person sees before approving: where it goes, the message, and
// every file that will be sent (up to a limit, with the total).
func publishApprovalPreview(workspace models.CloudWorkspace, args toolArgs, files []cloudworkspace.PublishFile) gin.H {
	branch, openPR := publishTarget(workspace, args)
	message, _ := args.str("message")
	listed := make([]gin.H, 0, min(len(files), publishPreviewFiles))
	for i, file := range files {
		if i == publishPreviewFiles {
			break
		}
		status := "changed"
		if file.Deleted {
			status = "deleted"
		}
		listed = append(listed, gin.H{"path": file.Path, "status": status})
	}
	return gin.H{
		"repository": workspace.GitHubOwner + "/" + workspace.GitHubRepo, "base_branch": workspace.BaseBranch,
		"branch": branch, "pull_request": openPR, "message": message, "files": listed, "file_count": len(files),
	}
}

// executePublishTool runs an approved workspace.publish. It sends only if the changes are still exactly
// the ones the person approved.
func executePublishTool(handle *workspaceHandle, args toolArgs) gin.H {
	approved, _ := args.str(publishFingerprintKey)
	if approved == "" {
		return gin.H{"ok": false, "error": "workspace.publish can only run after the user approves it"}
	}
	files, failure := collectPublishFiles(handle)
	if failure != nil {
		return gin.H{"ok": false, "error": failure.Message}
	}
	if changesFingerprint(files) != approved {
		return gin.H{"ok": false, "error": "Perubahan di workspace berbeda dari yang disetujui. Minta persetujuan lagi agar Anda melihat file yang akan dikirim."}
	}

	message, _ := args.str("message")
	branch, _ := args.str("branch")
	choice, _ := args.str("pull_request")
	title, _ := args.str("pr_title")
	body, _ := args.str("pr_body")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, failure := publishWorkspace(ctx, handle, publishInput{Branch: branch, Message: message, CreatePR: choice != "skip", PRTitle: title, PRBody: body}, files)
	if failure != nil {
		return gin.H{"ok": false, "error": failure.Message}
	}

	workspace := handle.Workspace
	userID := workspace.UserID
	audit := models.AuditLog{
		ActorUserID: &userID, ActorName: "agent", ActorRole: "user", Action: "workspace.github_published",
		EntityType: "workspace", EntityID: strconv.FormatUint(uint64(workspace.ID), 10), EntityName: workspace.Name,
		Message: publishAuditMessage(workspace, result, len(files), true), CreatedAt: time.Now(),
	}
	recordPublication(nil, workspace, result, len(files), audit)

	reply := gin.H{
		"ok": true, "repository": workspace.GitHubOwner + "/" + workspace.GitHubRepo, "branch": result.Branch,
		"file_count": len(files), "commit_url": result.CommitURL, "pull_request_url": result.PullRequestURL, "base_moved": result.BaseMoved,
	}
	if result.PullRequestError != "" {
		reply["pull_request_error"] = result.PullRequestError
	}
	return reply
}

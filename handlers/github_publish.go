package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/models"
)

const (
	publishMaxFiles      = 200
	publishMaxFileBytes  = 5 << 20
	publishMaxTotalBytes = 50 << 20
)

var branchNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)

// validBranchName follows git's rules closely enough for names people type here.
func validBranchName(name string) bool {
	return branchNamePattern.MatchString(name) &&
		!strings.Contains(name, "..") && !strings.Contains(name, "//") &&
		!strings.HasSuffix(name, "/") && !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, ".lock")
}

var branchNonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// defaultBranchName is agentic/<workspace>-<date-time>, which is unlikely to exist already.
func defaultBranchName(workspaceName string, now time.Time) string {
	slug := strings.Trim(branchNonSlug.ReplaceAllString(strings.ToLower(workspaceName), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "workspace"
	}
	return "agentic/" + slug + "-" + now.Format("20060102-150405")
}

func publishFail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"ok": false, "code": code, "message": message})
}

// publishInput is what a person (the "Send to GitHub" dialog) or an agent (workspace.publish) asks for.
type publishInput struct {
	Branch   string
	Message  string
	CreatePR bool
	PRTitle  string
	PRBody   string
}

type publishFailure struct {
	Status  int
	Code    string
	Message string
}

func (f *publishFailure) Error() string { return f.Message }

func failPublish(status int, code, message string) *publishFailure {
	return &publishFailure{Status: status, Code: code, Message: message}
}

// workspaceFromGitHub says whether a workspace knows where it came from, so changes can go back.
func workspaceFromGitHub(workspace models.CloudWorkspace) bool {
	return workspace.GitHubOwner != "" && workspace.GitHubRepo != "" && workspace.BaseBranch != "" && workspace.BaseSHA != ""
}

// collectPublishFiles reads the workspace's changes since the import through the workspace path checks
// (secret files and installed dependencies are already left out of the change list), within the limits.
func collectPublishFiles(handle *workspaceHandle) ([]cloudworkspace.PublishFile, *publishFailure) {
	changes, err := handle.changes()
	if err != nil {
		status, message := fileFailure(err)
		return nil, failPublish(status, "workspace_failed", message)
	}
	if len(changes) == 0 {
		return nil, failPublish(http.StatusBadRequest, "no_changes", "Tidak ada perubahan untuk dikirim.")
	}
	if len(changes) > publishMaxFiles {
		return nil, failPublish(http.StatusBadRequest, "too_many_changes", fmt.Sprintf("Terlalu banyak file berubah (maksimal %d sekaligus).", publishMaxFiles))
	}
	files := make([]cloudworkspace.PublishFile, 0, len(changes))
	var total int64
	for _, change := range changes {
		if change.Status == "deleted" {
			files = append(files, cloudworkspace.PublishFile{Path: change.Path, Deleted: true})
			continue
		}
		unreadable := failPublish(http.StatusBadRequest, "unreadable_file", "File "+change.Path+" tidak dapat dibaca.")
		absolute, _, err := cloudworkspace.Resolve(handle.FilesDir, change.Path)
		if err != nil {
			return nil, unreadable
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.Mode().IsRegular() {
			return nil, unreadable
		}
		if info.Size() > publishMaxFileBytes {
			return nil, failPublish(http.StatusBadRequest, "file_too_large", "File "+change.Path+" lebih besar dari 5 MiB.")
		}
		total += info.Size()
		if total > publishMaxTotalBytes {
			return nil, failPublish(http.StatusBadRequest, "too_large", "Total perubahan lebih besar dari 50 MiB.")
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			return nil, unreadable
		}
		files = append(files, cloudworkspace.PublishFile{Path: change.Path, Content: content, Added: change.Status == "added"})
	}
	return files, nil
}

// changesFingerprint identifies exactly what would be sent (paths, what happened to them, contents).
// An approval stores it, and the publish refuses to run if the changes differ by then, so the
// person never approves one set of changes and sends another.
func changesFingerprint(files []cloudworkspace.PublishFile) string {
	lines := make([]string, 0, len(files))
	for _, file := range files {
		if file.Deleted {
			lines = append(lines, file.Path+"\x00deleted")
			continue
		}
		sum := sha256.Sum256(file.Content)
		lines = append(lines, file.Path+"\x00"+hex.EncodeToString(sum[:]))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// publishWorkspace checks the request and sends the changes. It never writes to an existing branch.
func publishWorkspace(ctx context.Context, handle *workspaceHandle, input publishInput, files []cloudworkspace.PublishFile) (cloudworkspace.PublishResult, *publishFailure) {
	workspace := handle.Workspace
	if !workspaceFromGitHub(workspace) {
		return cloudworkspace.PublishResult{}, failPublish(http.StatusBadRequest, "not_from_github",
			"Workspace ini tidak berasal dari GitHub, atau diimpor sebelum asal repository dicatat. Impor ulang repository-nya untuk mengirim perubahan.")
	}
	token := githubTokenFor(workspace.UserID)
	if token == "" {
		return cloudworkspace.PublishResult{}, failPublish(http.StatusBadRequest, "github_not_connected", "Hubungkan GitHub di Settings > Integrasi terlebih dahulu.")
	}
	message := strings.TrimSpace(input.Message)
	if message == "" || len([]rune(message)) > 500 {
		return cloudworkspace.PublishResult{}, failPublish(http.StatusBadRequest, "invalid_message", "Pesan commit wajib diisi (maksimal 500 karakter).")
	}
	branch := strings.TrimSpace(input.Branch)
	if branch == "" {
		branch = defaultBranchName(workspace.Name, time.Now())
	}
	if !validBranchName(branch) || strings.EqualFold(branch, workspace.BaseBranch) {
		return cloudworkspace.PublishResult{}, failPublish(http.StatusBadRequest, "invalid_branch",
			"Nama branch tidak valid. Pakai huruf, angka, titik, garis bawah, tanda hubung, atau garis miring, dan jangan sama dengan branch asal.")
	}
	title := strings.TrimSpace(input.PRTitle)
	if title == "" {
		title = strings.SplitN(message, "\n", 2)[0]
	}
	if len([]rune(title)) > 200 || len([]rune(input.PRBody)) > 10000 {
		return cloudworkspace.PublishResult{}, failPublish(http.StatusBadRequest, "invalid_pull_request", "Judul pull request maksimal 200 karakter dan isinya maksimal 10.000 karakter.")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := cloudDownloader.Publish(ctx, token, cloudworkspace.PublishRequest{
		Owner: workspace.GitHubOwner, Repo: workspace.GitHubRepo, BaseBranch: workspace.BaseBranch, BaseSHA: workspace.BaseSHA,
		Branch: branch, Message: message, Files: files, CreatePR: input.CreatePR, PRTitle: title, PRBody: input.PRBody,
	})
	switch {
	case errors.Is(err, cloudworkspace.ErrGitHubToken):
		return result, failPublish(http.StatusBadRequest, "github_token_invalid", err.Error())
	case errors.Is(err, cloudworkspace.ErrBranchExists):
		return result, failPublish(http.StatusConflict, "branch_exists", err.Error())
	case errors.Is(err, cloudworkspace.ErrNoPushAccess):
		return result, failPublish(http.StatusForbidden, "no_push_access", err.Error())
	case errors.Is(err, cloudworkspace.ErrBaseGone):
		return result, failPublish(http.StatusConflict, "base_branch_gone", err.Error())
	case err != nil:
		return result, failPublish(http.StatusBadGateway, "github_failed", err.Error())
	}
	return result, nil
}

// recordPublication keeps the publish and its audit entry. The push already happened, so a failure
// to record is logged on the request and not reported as a failed publish.
func recordPublication(c *gin.Context, workspace models.CloudWorkspace, result cloudworkspace.PublishResult, fileCount int, audit models.AuditLog) {
	publication := models.GitHubPublication{
		UserID: workspace.UserID, WorkspaceID: workspace.ID, Repository: workspace.GitHubOwner + "/" + workspace.GitHubRepo,
		Branch: result.Branch, BaseBranch: workspace.BaseBranch, CommitSHA: result.CommitSHA, CommitURL: result.CommitURL,
		PullRequestURL: result.PullRequestURL, FileCount: fileCount,
	}
	if err := config.DB.Create(&publication).Error; err != nil && c != nil {
		c.Error(err)
	}
	_ = config.DB.Create(&audit).Error
}

func publishAuditMessage(workspace models.CloudWorkspace, result cloudworkspace.PublishResult, fileCount int, byAgent bool) string {
	who := ""
	if byAgent {
		who = " oleh agent (disetujui pengguna)"
	}
	return fmt.Sprintf("Perubahan (%d file) dikirim ke %s/%s branch %s%s.", fileCount, workspace.GitHubOwner, workspace.GitHubRepo, result.Branch, who)
}

// PublishWorkspaceToGitHub sends the workspace's changes since the import to GitHub: one commit on a
// new branch made from the imported commit, and (by default) a pull request into the branch the
// workspace came from. It never writes to an existing branch.
func PublishWorkspaceToGitHub(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	var req struct {
		Branch   string `json:"branch"`
		Message  string `json:"message"`
		CreatePR *bool  `json:"create_pr"`
		PRTitle  string `json:"pr_title"`
		PRBody   string `json:"pr_body"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		publishFail(c, http.StatusBadRequest, "invalid_request", "Format permintaan tidak valid.")
		return
	}
	if !workspaceFromGitHub(handle.Workspace) {
		failure := failPublish(http.StatusBadRequest, "not_from_github",
			"Workspace ini tidak berasal dari GitHub, atau diimpor sebelum asal repository dicatat. Impor ulang repository-nya untuk mengirim perubahan.")
		publishFail(c, failure.Status, failure.Code, failure.Message)
		return
	}
	files, failure := collectPublishFiles(handle)
	if failure != nil {
		publishFail(c, failure.Status, failure.Code, failure.Message)
		return
	}
	input := publishInput{Branch: req.Branch, Message: req.Message, CreatePR: req.CreatePR == nil || *req.CreatePR, PRTitle: req.PRTitle, PRBody: req.PRBody}
	result, failure := publishWorkspace(c.Request.Context(), handle, input, files)
	if failure != nil {
		publishFail(c, failure.Status, failure.Code, failure.Message)
		return
	}

	workspace := handle.Workspace
	audit := newAuditLog(c, "workspace.github_published", "workspace", strconv.FormatUint(uint64(workspace.ID), 10), workspace.Name,
		publishAuditMessage(workspace, result, len(files), false))
	recordPublication(c, workspace, result, len(files), audit)
	c.JSON(http.StatusOK, gin.H{"ok": true, "repository": workspace.GitHubOwner + "/" + workspace.GitHubRepo, "file_count": len(files), "result": result})
}

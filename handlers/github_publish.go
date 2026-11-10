package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
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

// PublishWorkspaceToGitHub sends the workspace's changes since the import to GitHub: one commit on a
// new branch made from the imported commit, and (by default) a pull request into the branch the
// workspace came from. It never writes to an existing branch.
func PublishWorkspaceToGitHub(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	workspace := handle.Workspace
	if workspace.GitHubOwner == "" || workspace.GitHubRepo == "" || workspace.BaseBranch == "" || workspace.BaseSHA == "" {
		publishFail(c, http.StatusBadRequest, "not_from_github",
			"Workspace ini tidak berasal dari GitHub, atau diimpor sebelum asal repository dicatat. Impor ulang repository-nya untuk mengirim perubahan.")
		return
	}
	token := githubTokenFor(workspace.UserID)
	if token == "" {
		publishFail(c, http.StatusBadRequest, "github_not_connected", "Hubungkan GitHub di Settings > Integrasi terlebih dahulu.")
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
	message := strings.TrimSpace(req.Message)
	if message == "" || len([]rune(message)) > 500 {
		publishFail(c, http.StatusBadRequest, "invalid_message", "Pesan commit wajib diisi (maksimal 500 karakter).")
		return
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = defaultBranchName(workspace.Name, time.Now())
	}
	if !validBranchName(branch) || strings.EqualFold(branch, workspace.BaseBranch) {
		publishFail(c, http.StatusBadRequest, "invalid_branch",
			"Nama branch tidak valid. Pakai huruf, angka, titik, garis bawah, tanda hubung, atau garis miring, dan jangan sama dengan branch asal.")
		return
	}
	createPR := req.CreatePR == nil || *req.CreatePR
	title := strings.TrimSpace(req.PRTitle)
	if title == "" {
		title = strings.SplitN(message, "\n", 2)[0]
	}
	if len([]rune(title)) > 200 || len([]rune(req.PRBody)) > 10000 {
		publishFail(c, http.StatusBadRequest, "invalid_pull_request", "Judul pull request maksimal 200 karakter dan isinya maksimal 10.000 karakter.")
		return
	}

	changes, err := handle.changes()
	if err != nil {
		status, msg := fileFailure(err)
		workspaceError(c, status, msg)
		return
	}
	if len(changes) == 0 {
		publishFail(c, http.StatusBadRequest, "no_changes", "Tidak ada perubahan untuk dikirim.")
		return
	}
	if len(changes) > publishMaxFiles {
		publishFail(c, http.StatusBadRequest, "too_many_changes", fmt.Sprintf("Terlalu banyak file berubah (maksimal %d sekaligus).", publishMaxFiles))
		return
	}

	// Read the changed files through the workspace's own path checks (no escaping the root, no
	// symlinks). Secret files and installed dependencies are already left out of the change list.
	files := make([]cloudworkspace.PublishFile, 0, len(changes))
	var total int64
	for _, change := range changes {
		if change.Status == "deleted" {
			files = append(files, cloudworkspace.PublishFile{Path: change.Path, Deleted: true})
			continue
		}
		absolute, _, err := cloudworkspace.Resolve(handle.FilesDir, change.Path)
		if err != nil {
			publishFail(c, http.StatusBadRequest, "unreadable_file", "File "+change.Path+" tidak dapat dibaca.")
			return
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.Mode().IsRegular() {
			publishFail(c, http.StatusBadRequest, "unreadable_file", "File "+change.Path+" tidak dapat dibaca.")
			return
		}
		if info.Size() > publishMaxFileBytes {
			publishFail(c, http.StatusBadRequest, "file_too_large", "File "+change.Path+" lebih besar dari 5 MiB.")
			return
		}
		total += info.Size()
		if total > publishMaxTotalBytes {
			publishFail(c, http.StatusBadRequest, "too_large", "Total perubahan lebih besar dari 50 MiB.")
			return
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			publishFail(c, http.StatusBadRequest, "unreadable_file", "File "+change.Path+" tidak dapat dibaca.")
			return
		}
		files = append(files, cloudworkspace.PublishFile{Path: change.Path, Content: content})
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	result, err := cloudDownloader.Publish(ctx, token, cloudworkspace.PublishRequest{
		Owner: workspace.GitHubOwner, Repo: workspace.GitHubRepo, BaseBranch: workspace.BaseBranch, BaseSHA: workspace.BaseSHA,
		Branch: branch, Message: message, Files: files, CreatePR: createPR, PRTitle: title, PRBody: req.PRBody,
	})
	switch {
	case errors.Is(err, cloudworkspace.ErrGitHubToken):
		publishFail(c, http.StatusBadRequest, "github_token_invalid", err.Error())
		return
	case errors.Is(err, cloudworkspace.ErrBranchExists):
		publishFail(c, http.StatusConflict, "branch_exists", err.Error())
		return
	case errors.Is(err, cloudworkspace.ErrNoPushAccess):
		publishFail(c, http.StatusForbidden, "no_push_access", err.Error())
		return
	case errors.Is(err, cloudworkspace.ErrBaseGone):
		publishFail(c, http.StatusConflict, "base_branch_gone", err.Error())
		return
	case err != nil:
		publishFail(c, http.StatusBadGateway, "github_failed", err.Error())
		return
	}

	repository := workspace.GitHubOwner + "/" + workspace.GitHubRepo
	publication := models.GitHubPublication{
		UserID: workspace.UserID, WorkspaceID: workspace.ID, Repository: repository, Branch: result.Branch, BaseBranch: workspace.BaseBranch,
		CommitSHA: result.CommitSHA, CommitURL: result.CommitURL, PullRequestURL: result.PullRequestURL, FileCount: len(files),
	}
	audit := newAuditLog(c, "workspace.github_published", "workspace", strconv.FormatUint(uint64(workspace.ID), 10), workspace.Name,
		fmt.Sprintf("Perubahan (%d file) dikirim ke %s branch %s.", len(files), repository, result.Branch))
	if err := config.DB.Create(&publication).Error; err != nil {
		c.Error(err) // the push happened; only the record failed
	}
	_ = config.DB.Create(&audit).Error

	c.JSON(http.StatusOK, gin.H{"ok": true, "repository": repository, "file_count": len(files), "result": result})
}

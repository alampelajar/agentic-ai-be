package localrunner

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type cloneRequest struct {
	URL string `json:"url" binding:"required"`
}

var repoSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (s *Server) cloneGitHub(c *gin.Context) {
	var req cloneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "GitHub repository URL is required"})
		return
	}
	repoURL, owner, repo, err := validatePublicGitHubURL(req.URL)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := "github-" + strings.ToLower(owner) + "-" + strings.ToLower(repo)
	for _, w := range s.cfg.Workspaces {
		if w.ID == id {
			c.JSON(409, gin.H{"error": "repository is already registered as a workspace"})
			return
		}
	}
	target := filepath.Join(s.cfg.GitRoot, id)
	if _, err := os.Stat(target); err == nil {
		c.JSON(409, gin.H{"error": "workspace directory already exists; remove or rename it before cloning"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--", repoURL, target)
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		_ = os.RemoveAll(target)
		message := "git clone failed; verify Git is installed and the repository is public"
		if ctx.Err() != nil {
			message = "git clone timed out after 3 minutes"
		}
		c.JSON(400, gin.H{"error": message, "details": truncateOutput(output)})
		return
	}

	workspace := Workspace{ID: id, Name: owner + "/" + repo, Path: target}
	dynamicWorkspaces := append(append([]Workspace(nil), s.cfg.DynamicWorkspaces...), workspace)
	if err := persistClonedWorkspaces(s.cfg.WorkspaceRegistryPath, dynamicWorkspaces); err != nil {
		_ = os.RemoveAll(target)
		c.JSON(500, gin.H{"error": "repository was cloned but its workspace registration could not be saved"})
		return
	}

	s.cfg.DynamicWorkspaces = dynamicWorkspaces
	s.cfg.Workspaces = append(s.cfg.Workspaces, workspace)
	c.JSON(201, gin.H{
		"workspace": gin.H{"id": workspace.ID, "name": workspace.Name},
		"message":   "repository cloned and workspace registration saved",
	})
}

func validatePublicGitHubURL(raw string) (string, string, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || strings.ToLower(u.Hostname()) != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", "", errors.New("only public https://github.com/owner/repository URLs are supported")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || !repoSegment.MatchString(parts[0]) || !repoSegment.MatchString(parts[1]) || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", "", "", errors.New("URL must have exactly the form https://github.com/owner/repository")
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	if repo == "" {
		return "", "", "", errors.New("invalid repository name")
	}
	return "https://github.com/" + parts[0] + "/" + repo + ".git", parts[0], repo, nil
}

func truncateOutput(b []byte) string {
	const limit = 4096
	if len(b) > limit {
		return string(b[:limit]) + "\n...[truncated]"
	}
	return string(b)
}

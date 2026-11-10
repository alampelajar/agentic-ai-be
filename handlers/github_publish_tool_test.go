package handlers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

func TestPublishToolIsCodingOnlyAndAlwaysNeedsApproval(t *testing.T) {
	if !services.CloudToolNeedsApproval(services.ToolWorkspacePublish) {
		t.Fatal("workspace.publish must always need the user's approval")
	}
	if !services.CloudToolAllowedForAgent(services.ToolWorkspacePublish, "coding") {
		t.Error("the coding agent should be able to ask to publish")
	}
	for _, slug := range []string{"review", "planning", "testing", "debugging", "code-review", ""} {
		if services.CloudToolAllowedForAgent(services.ToolWorkspacePublish, slug) {
			t.Errorf("%q must not be able to publish", slug)
		}
	}
}

func TestPublishToolIsOnlyOfferedForAWorkspaceFromGitHub(t *testing.T) {
	has := func(tools []string) bool {
		for _, tool := range tools {
			if tool == services.ToolWorkspacePublish {
				return true
			}
		}
		return false
	}
	if has(services.CloudToolsForWorkspace("coding", false)) {
		t.Error("a workspace not from GitHub has nowhere to publish to")
	}
	if !has(services.CloudToolsForWorkspace("coding", true)) {
		t.Error("a GitHub workspace should offer publish to the coding agent")
	}
}

func TestAModelCannotSupplyTheApprovedFingerprint(t *testing.T) {
	_, err := normalizeToolArgs(services.ToolWorkspacePublish, map[string]any{"message": "x", publishFingerprintKey: "forged"}, models.CloudWorkspace{})
	if err == nil {
		t.Fatal("the fingerprint is set by the server only; a model-supplied one must be refused")
	}
	if _, err := normalizeToolArgs(services.ToolWorkspacePublish, map[string]any{"message": "x", "pull_request": "maybe"}, models.CloudWorkspace{}); err == nil {
		t.Error("pull_request accepts only open or skip")
	}
	if _, err := normalizeToolArgs(services.ToolWorkspacePublish, map[string]any{}, models.CloudWorkspace{}); err == nil {
		t.Error("a commit message is required")
	}
	if _, err := normalizeToolArgs(services.ToolWorkspacePublish, map[string]any{"message": "Update README", "pull_request": "skip", "branch": "agentic/x"}, models.CloudWorkspace{}); err != nil {
		t.Errorf("valid arguments refused: %v", err)
	}
}

func TestChangesFingerprintPinsTheExactChanges(t *testing.T) {
	a := []cloudworkspace.PublishFile{{Path: "a.txt", Content: []byte("one")}, {Path: "b.txt", Deleted: true}}
	reordered := []cloudworkspace.PublishFile{{Path: "b.txt", Deleted: true}, {Path: "a.txt", Content: []byte("one")}}
	if changesFingerprint(a) != changesFingerprint(reordered) {
		t.Error("the order of the change list must not matter")
	}
	for name, other := range map[string][]cloudworkspace.PublishFile{
		"content":   {{Path: "a.txt", Content: []byte("two")}, {Path: "b.txt", Deleted: true}},
		"deletion":  {{Path: "a.txt", Content: []byte("one")}},
		"extra":     append([]cloudworkspace.PublishFile{{Path: "c.txt", Content: []byte("x")}}, a...),
		"undeleted": {{Path: "a.txt", Content: []byte("one")}, {Path: "b.txt", Content: []byte("")}},
	} {
		if changesFingerprint(other) == changesFingerprint(a) {
			t.Errorf("a different %s must give a different fingerprint", name)
		}
	}
}

func TestPublishToolRefusesToRunWithoutAnApproval(t *testing.T) {
	result := executePublishTool(nil, toolArgs{"message": "x"})
	if result["ok"] != false || !strings.Contains(fmt.Sprint(result["error"]), "approve") {
		t.Fatalf("got %v", result)
	}
}

func TestPublishApprovalShowsWhereItGoesAndEveryFile(t *testing.T) {
	workspace := models.CloudWorkspace{GitHubOwner: "o", GitHubRepo: "r", BaseBranch: "main"}
	files := make([]cloudworkspace.PublishFile, 0, 60)
	for i := 0; i < 60; i++ {
		files = append(files, cloudworkspace.PublishFile{Path: fmt.Sprintf("f%02d.txt", i), Content: []byte("x")})
	}
	files[1].Deleted = true
	args := toolArgs{"message": "Update docs", "branch": "agentic/docs"}

	summary := publishApprovalSummary(workspace, args, files)
	for _, part := range []string{"60 changed file", "o/r", "agentic/docs", "pull request into main"} {
		if !strings.Contains(summary, part) {
			t.Errorf("summary %q lacks %q", summary, part)
		}
	}
	if strings.Contains(publishApprovalSummary(workspace, toolArgs{"message": "x", "pull_request": "skip"}, files), "pull request") {
		t.Error("no pull request was asked for")
	}

	preview := publishApprovalPreview(workspace, args, files)
	listed := preview["files"].([]gin.H)
	if preview["file_count"] != 60 || len(listed) != publishPreviewFiles || preview["message"] != "Update docs" || preview["repository"] != "o/r" {
		t.Errorf("preview = %v", preview)
	}
	if listed[1]["status"] != "deleted" {
		t.Errorf("a deleted file must show as deleted: %v", listed[1])
	}
}

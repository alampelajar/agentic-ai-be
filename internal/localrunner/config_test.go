package localrunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistedWorkspaceSurvivesReload(t *testing.T) {
	gitRoot := t.TempDir()
	workspaceDir := filepath.Join(gitRoot, "github-alampelajar-agentic-ai-fe")
	if err := os.MkdirAll(workspaceDir, 0750); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(gitRoot, ".local-agent-runner-workspaces.json")
	cloned := []Workspace{{
		ID:   "github-alampelajar-agentic-ai-fe",
		Name: "alampelajar/agentic-ai-fe",
		Path: workspaceDir,
	}}
	if err := persistClonedWorkspaces(registry, cloned); err != nil {
		t.Fatalf("persist workspace registry: %v", err)
	}

	got, err := loadPersistedWorkspaces(registry, gitRoot, map[string]bool{})
	if err != nil {
		t.Fatalf("reload workspace registry: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one restored workspace, got %d", len(got))
	}
	if got[0].ID != cloned[0].ID || got[0].Name != cloned[0].Name || got[0].Path != workspaceDir {
		t.Fatalf("restored workspace mismatch: %#v", got[0])
	}
}

func TestPersistedWorkspaceRejectsPathOutsideGitRoot(t *testing.T) {
	gitRoot := t.TempDir()
	outside := t.TempDir()
	registry := filepath.Join(gitRoot, ".local-agent-runner-workspaces.json")
	cloned := []Workspace{{
		ID:   "github-example-repo",
		Name: "example/repo",
		Path: filepath.Join(outside, "repo"),
	}}
	if err := persistClonedWorkspaces(registry, cloned); err != nil {
		t.Fatal(err)
	}

	if _, err := loadPersistedWorkspaces(registry, gitRoot, map[string]bool{}); err == nil {
		t.Fatal("expected registry path outside Git root to be rejected")
	}
}

func TestPersistedWorkspaceIgnoresConfiguredID(t *testing.T) {
	gitRoot := t.TempDir()
	workspaceDir := filepath.Join(gitRoot, "github-example-repo")
	if err := os.MkdirAll(workspaceDir, 0750); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(gitRoot, ".local-agent-runner-workspaces.json")
	cloned := []Workspace{{
		ID:   "github-example-repo",
		Name: "example/repo",
		Path: workspaceDir,
	}}
	if err := persistClonedWorkspaces(registry, cloned); err != nil {
		t.Fatal(err)
	}

	got, err := loadPersistedWorkspaces(registry, gitRoot, map[string]bool{"github-example-repo": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected environment-configured workspace to take precedence, got %#v", got)
	}
}

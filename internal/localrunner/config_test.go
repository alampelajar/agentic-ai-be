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

func TestLoadConfigRestoresClonedWorkspaces(t *testing.T) {
	gitRoot := t.TempDir()
	workspaceID := "github-example-repo"
	workspacePath := filepath.Join(gitRoot, workspaceID)
	if err := os.MkdirAll(workspacePath, 0750); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(gitRoot, ".local-agent-runner-workspaces.json")
	persisted := []Workspace{{
		ID:   workspaceID,
		Name: "example/repo",
		Path: workspacePath,
	}}
	if err := persistClonedWorkspaces(registryPath, persisted); err != nil {
		t.Fatalf("persist workspace registry: %v", err)
	}

	t.Setenv("LOCAL_RUNNER_TOKEN", "test-token-with-at-least-32-characters")
	t.Setenv("LOCAL_RUNNER_WORKSPACES_JSON", "[]")
	t.Setenv("LOCAL_RUNNER_ADDR", "127.0.0.1:8765")
	t.Setenv("LOCAL_RUNNER_ALLOWED_ORIGINS", "http://localhost:5173")
	t.Setenv("LOCAL_RUNNER_GIT_ROOT", gitRoot)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load runner config after restart: %v", err)
	}

	if len(cfg.Workspaces) != 1 {
		t.Fatalf("expected one restored workspace, got %d", len(cfg.Workspaces))
	}
	if len(cfg.DynamicWorkspaces) != 1 {
		t.Fatalf("expected one dynamic workspace, got %d", len(cfg.DynamicWorkspaces))
	}
	if cfg.Workspaces[0].ID != workspaceID || cfg.Workspaces[0].Name != "example/repo" {
		t.Fatalf("unexpected restored workspace: %#v", cfg.Workspaces[0])
	}
	if cfg.WorkspaceRegistryPath != registryPath {
		t.Fatalf("unexpected registry path: got %q, want %q", cfg.WorkspaceRegistryPath, registryPath)
	}
}


package localrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type Config struct {
	ListenAddress         string
	Token                 string
	Workspaces            []Workspace
	DynamicWorkspaces     []Workspace
	WorkspaceRegistryPath string
	AllowedOrigins        []string
	GitRoot               string
}

func LoadConfig() (Config, error) {
	token := strings.TrimSpace(os.Getenv("LOCAL_RUNNER_TOKEN"))
	if len(token) < 32 {
		return Config{}, errors.New("LOCAL_RUNNER_TOKEN must be set to a random value of at least 32 characters")
	}

	raw := strings.TrimSpace(os.Getenv("LOCAL_RUNNER_WORKSPACES_JSON"))
	if raw == "" {
		return Config{}, errors.New("LOCAL_RUNNER_WORKSPACES_JSON must be a JSON array of workspace objects")
	}

	var workspaces []Workspace
	if err := json.Unmarshal([]byte(raw), &workspaces); err != nil {
		return Config{}, fmt.Errorf("parse LOCAL_RUNNER_WORKSPACES_JSON: %w", err)
	}

	seen := map[string]bool{}
	for i := range workspaces {
		w := &workspaces[i]
		w.ID = strings.TrimSpace(w.ID)
		w.Name = strings.TrimSpace(w.Name)
		w.Path = strings.TrimSpace(w.Path)
		if w.ID == "" || w.Name == "" || w.Path == "" {
			return Config{}, fmt.Errorf("workspace %d requires id, name and path", i)
		}
		if seen[w.ID] {
			return Config{}, fmt.Errorf("duplicate workspace id %q", w.ID)
		}
		seen[w.ID] = true
	}

	addr := strings.TrimSpace(os.Getenv("LOCAL_RUNNER_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8765"
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") &&
		!strings.HasPrefix(addr, "localhost:") &&
		!strings.HasPrefix(addr, "[::1]:") {
		return Config{}, errors.New("runner must bind to loopback only (127.0.0.1, localhost or ::1)")
	}

	origins := strings.FieldsFunc(os.Getenv("LOCAL_RUNNER_ALLOWED_ORIGINS"), func(r rune) bool {
		return r == ','
	})
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}
	if len(origins) == 0 {
		origins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	}

	gitRoot := strings.TrimSpace(os.Getenv("LOCAL_RUNNER_GIT_ROOT"))
	if gitRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, err
		}
		gitRoot = filepath.Join(home, "AgenticAIWorkspaces")
	}
	gitRoot, err := filepath.Abs(gitRoot)
	if err != nil {
		return Config{}, fmt.Errorf("resolve Git workspace root: %w", err)
	}
	if err := os.MkdirAll(gitRoot, 0750); err != nil {
		return Config{}, fmt.Errorf("create Git workspace root: %w", err)
	}

	registryPath := filepath.Join(gitRoot, ".local-agent-runner-workspaces.json")
	dynamicWorkspaces, err := loadPersistedWorkspaces(registryPath, gitRoot, seen)
	if err != nil {
		return Config{}, fmt.Errorf("load cloned workspace registry: %w", err)
	}
	workspaces = append(workspaces, dynamicWorkspaces...)

	return Config{
		ListenAddress:         addr,
		Token:                 token,
		Workspaces:            workspaces,
		DynamicWorkspaces:     dynamicWorkspaces,
		WorkspaceRegistryPath: registryPath,
		AllowedOrigins:        origins,
		GitRoot:               gitRoot,
	}, nil
}

func loadPersistedWorkspaces(path, gitRoot string, configuredIDs map[string]bool) ([]Workspace, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var persisted []Workspace
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, fmt.Errorf("invalid workspace registry JSON: %w", err)
	}

	canonicalGitRoot, err := filepath.EvalSymlinks(gitRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve Git workspace root: %w", err)
	}

	seen := make(map[string]bool, len(persisted))
	result := make([]Workspace, 0, len(persisted))
	for i, workspace := range persisted {
		workspace.ID = strings.TrimSpace(workspace.ID)
		workspace.Name = strings.TrimSpace(workspace.Name)
		workspace.Path = strings.TrimSpace(workspace.Path)
		if !strings.HasPrefix(workspace.ID, "github-") ||
			workspace.ID == "github-" ||
			filepath.Base(workspace.ID) != workspace.ID ||
			strings.ContainsAny(workspace.ID, `/\\:`) ||
			workspace.ID == "." || workspace.ID == ".." ||
			workspace.Name == "" || workspace.Path == "" {
			return nil, fmt.Errorf("invalid cloned workspace entry at index %d", i)
		}
		if seen[workspace.ID] {
			return nil, fmt.Errorf("duplicate cloned workspace id %q", workspace.ID)
		}
		seen[workspace.ID] = true
		if configuredIDs[workspace.ID] {
			// Explicit environment configuration takes precedence over the saved registry.
			continue
		}

		expectedPath := filepath.Join(gitRoot, workspace.ID)
		actualPath, err := filepath.Abs(workspace.Path)
		if err != nil {
			return nil, fmt.Errorf("resolve path for workspace %q: %w", workspace.ID, err)
		}
		expectedPath, err = filepath.Abs(expectedPath)
		if err != nil {
			return nil, err
		}
		actualClean, expectedClean := filepath.Clean(actualPath), filepath.Clean(expectedPath)
		samePath := actualClean == expectedClean
		if runtime.GOOS == "windows" {
			samePath = strings.EqualFold(actualClean, expectedClean)
		}
		if !samePath {
			return nil, fmt.Errorf("cloned workspace %q has a path outside the configured Git root", workspace.ID)
		}

		info, err := os.Stat(expectedPath)
		if errors.Is(err, os.ErrNotExist) {
			// A directory manually removed by the user should not break runner startup.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("check cloned workspace %q: %w", workspace.ID, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("cloned workspace %q is not a directory", workspace.ID)
		}

		// Do not trust a registry entry merely because its path string is under
		// GitRoot: the workspace directory itself could have been replaced by a
		// symlink pointing elsewhere. Require its resolved path to match the
		// expected child directory of the resolved GitRoot.
		resolvedWorkspacePath, err := filepath.EvalSymlinks(expectedPath)
		if err != nil {
			return nil, fmt.Errorf("resolve cloned workspace %q: %w", workspace.ID, err)
		}
		canonicalExpectedPath := filepath.Join(canonicalGitRoot, workspace.ID)
		resolvedClean := filepath.Clean(resolvedWorkspacePath)
		canonicalExpectedClean := filepath.Clean(canonicalExpectedPath)
		sameResolvedPath := resolvedClean == canonicalExpectedClean
		if runtime.GOOS == "windows" {
			sameResolvedPath = strings.EqualFold(resolvedClean, canonicalExpectedClean)
		}
		if !sameResolvedPath {
			return nil, fmt.Errorf("cloned workspace %q resolves outside its expected directory", workspace.ID)
		}

		workspace.Path = expectedPath
		result = append(result, workspace)
	}
	return result, nil
}

func persistClonedWorkspaces(path string, workspaces []Workspace) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("workspace registry path is not configured")
	}
	data, err := json.MarshalIndent(workspaces, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

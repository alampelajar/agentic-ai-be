package localrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type Config struct {
	ListenAddress string
	Token         string
	Workspaces    []Workspace
	AllowedOrigins []string
	GitRoot string
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
	if !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "localhost:") && !strings.HasPrefix(addr, "[::1]:") {
		return Config{}, errors.New("runner must bind to loopback only (127.0.0.1, localhost or ::1)")
	}
	origins := strings.FieldsFunc(os.Getenv("LOCAL_RUNNER_ALLOWED_ORIGINS"), func(r rune) bool { return r == ',' })
	if len(origins) == 0 {
		origins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	}
	gitRoot := strings.TrimSpace(os.Getenv("LOCAL_RUNNER_GIT_ROOT"))
	if gitRoot == "" { home, err := os.UserHomeDir(); if err != nil { return Config{}, err }; gitRoot = home + string(os.PathSeparator) + "AgenticAIWorkspaces" }
	if err := os.MkdirAll(gitRoot, 0750); err != nil { return Config{}, fmt.Errorf("create Git workspace root: %w", err) }
	return Config{ListenAddress: addr, Token: token, Workspaces: workspaces, AllowedOrigins: origins, GitRoot: gitRoot}, nil
}

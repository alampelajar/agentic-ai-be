package localrunner

import (
	"fmt"
	"path/filepath"
)

// dockerAction returns fixed container commands only. It never passes arbitrary
// user-supplied shell text into Docker. Containers have no network, a read-only
// root filesystem, resource limits, and no-new-privileges enabled.
func dockerAction(root, action string) ([]string, error) {
	var image string
	var command []string
	switch action {
	case "go-test":
		if !exists(filepath.Join(root, "go.mod")) { break }
		image, command = "golang:1.27", []string{"go", "test", "./..."}
	case "go-build":
		if !exists(filepath.Join(root, "go.mod")) { break }
		image, command = "golang:1.27", []string{"go", "build", "./..."}
	case "npm-test", "npm-build", "npm-lint":
		if !exists(filepath.Join(root, "package.json")) { break }
		script := map[string]string{"npm-test":"test", "npm-build":"build", "npm-lint":"lint"}[action]
		image, command = "node:22-bookworm", []string{"npm", "run", script}
	case "python-test":
		if !exists(filepath.Join(root, "pyproject.toml")) && !exists(filepath.Join(root, "pytest.ini")) && !exists(filepath.Join(root, "tests")) { break }
		image, command = "python:3.12-slim", []string{"python", "-m", "pytest"}
	default:
		return nil, fmt.Errorf("Docker action %q is not supported; supported: go-test, go-build, npm-test, npm-build, npm-lint, python-test", action)
	}
	if image == "" {
		return nil, fmt.Errorf("action %q does not match a supported project manifest", action)
	}
	// The workspace is intentionally mounted writable so build tools can create
	// outputs. Use a disposable container and isolated temp/cache directories.
	args := []string{
		"run", "--rm", "--network", "none",
		"--memory", "1g", "--cpus", "1", "--pids-limit", "128",
		"--security-opt", "no-new-privileges",
		"--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m",
		"-v", root + ":/workspace", "-w", "/workspace",
		image,
	}
	return append(args, command...), nil
}

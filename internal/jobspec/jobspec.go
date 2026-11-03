// Package jobspec is the allowlist of commands a Cloud Workspace job may run. It
// only describes commands; it executes nothing and imports nothing that could, so
// the API server can use it to validate requests while only the separate
// execution worker (package cloudexec) runs them.
package jobspec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CommandSpec is a fixed command: an image and an argument array. There is no
// shell and nothing in it comes from a client or a model.
type CommandSpec struct {
	JobType string
	Runtime string
	Image   string
	Argv    []string
	// Then holds further fixed commands run after Argv, one fresh container each, in
	// order; the job stops at the first one that fails. Only dependency
	// installation uses it (go mod tidy resolves go.sum, go mod vendor copies the
	// sources the network-less test jobs build from).
	Then  [][]string
	Label   string // what the console shows, e.g. "go test ./..."
	Default time.Duration
	// Install marks a dependency installation. It is the only kind of job that gets
	// a network, and then only the registry-only one: see DockerConfig.InstallNetwork.
	// An install command never runs project code (no npm scripts, no source builds).
	Install bool
}

// DependencyDir is where a pip install puts packages inside the workspace.
const DependencyDir = ".pydeps"

const (
	MinTimeout = 10 * time.Second
	MaxTimeout = 10 * time.Minute
)

// Images are the only images a job may use. A deployment can pin different tags
// with CLOUD_EXEC_IMAGE_GO, _NODE and _PYTHON; whatever is chosen is still this
// fixed set of three, never a value from a request.
type Images struct {
	Go, Node, Python string
}

func DefaultImages() Images {
	return Images{Go: "golang:1.27", Node: "node:22-bookworm", Python: "python:3.12-slim"}
}

func (i Images) Approved(image string) bool {
	return image != "" && (image == i.Go || image == i.Node || image == i.Python)
}

var jobTypes = []string{"go-test", "go-build", "npm-test", "npm-build", "npm-lint", "python-test", "go-deps", "npm-install", "pip-install"}

// JobTypes lists what can be requested.
func JobTypes() []string { return append([]string(nil), jobTypes...) }

var ErrUnknownJobType = errors.New("unsupported job type")

// Resolve turns a job type into its fixed command, checking that the project has
// the files that command needs. filesDir may be empty to skip that check.
func Resolve(jobType string, images Images, filesDir string) (CommandSpec, error) {
	has := func(names ...string) bool {
		if filesDir == "" {
			return true
		}
		for _, name := range names {
			if _, err := os.Stat(filepath.Join(filesDir, name)); err == nil {
				return true
			}
		}
		return false
	}
	var spec CommandSpec
	switch jobType {
	case "go-test":
		spec = CommandSpec{JobType: jobType, Runtime: "go", Image: images.Go, Argv: []string{"go", "test", "./..."}, Label: "go test ./...", Default: 3 * time.Minute}
		if !has("go.mod") {
			return CommandSpec{}, errors.New("this workspace has no go.mod")
		}
	case "go-build":
		spec = CommandSpec{JobType: jobType, Runtime: "go", Image: images.Go, Argv: []string{"go", "build", "./..."}, Label: "go build ./...", Default: 3 * time.Minute}
		if !has("go.mod") {
			return CommandSpec{}, errors.New("this workspace has no go.mod")
		}
	case "npm-test", "npm-build", "npm-lint":
		script := map[string]string{"npm-test": "test", "npm-build": "build", "npm-lint": "lint"}[jobType]
		spec = CommandSpec{JobType: jobType, Runtime: "node", Image: images.Node, Argv: []string{"npm", "run", script, "--silent"}, Label: "npm run " + script, Default: 3 * time.Minute}
		if !has("package.json") {
			return CommandSpec{}, errors.New("this workspace has no package.json")
		}
	case "python-test":
		spec = CommandSpec{JobType: jobType, Runtime: "python", Image: images.Python, Argv: []string{"python", "-m", "pytest", "-q"}, Label: "python -m pytest -q", Default: 3 * time.Minute}
		if !has("pyproject.toml", "pytest.ini", "tests") {
			return CommandSpec{}, errors.New("this workspace has no pytest configuration or tests folder")
		}
	case "go-deps":
		// Vendoring is how Go dependencies reach the network-less test jobs: they
		// build from vendor/ (see environmentFor in package cloudexec).
		spec = CommandSpec{JobType: jobType, Runtime: "go", Image: images.Go, Argv: []string{"go", "mod", "tidy"}, Then: [][]string{{"go", "mod", "vendor"}}, Label: "go mod tidy, go mod vendor", Default: 5 * time.Minute, Install: true}
		if !has("go.mod") {
			return CommandSpec{}, errors.New("this workspace has no go.mod")
		}
	case "npm-install":
		spec = CommandSpec{JobType: jobType, Runtime: "node", Image: images.Node, Argv: []string{"npm", "install", "--ignore-scripts", "--no-audit", "--no-fund", "--registry=https://registry.npmjs.org/"}, Label: "npm install --ignore-scripts", Default: 5 * time.Minute, Install: true}
		if !has("package.json") {
			return CommandSpec{}, errors.New("this workspace has no package.json")
		}
	case "pip-install":
		argv := []string{"python", "-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--only-binary=:all:", "--upgrade", "--index-url", "https://pypi.org/simple", "--target", "/workspace/" + DependencyDir, "pytest"}
		label := "pip install pytest"
		if filesDir != "" && has("requirements.txt") {
			argv = append(argv, "-r", "requirements.txt")
			label += " -r requirements.txt"
		}
		spec = CommandSpec{JobType: jobType, Runtime: "python", Image: images.Python, Argv: argv, Label: label, Default: 5 * time.Minute, Install: true}
		if !has("requirements.txt", "pyproject.toml", "pytest.ini", "tests") {
			return CommandSpec{}, errors.New("this workspace has no requirements.txt, pytest configuration or tests folder")
		}
	default:
		return CommandSpec{}, fmt.Errorf("%w: %q (supported: %s)", ErrUnknownJobType, jobType, strings.Join(jobTypes, ", "))
	}
	return spec, nil
}

// ClampTimeout keeps a requested timeout inside the allowed range; 0 means the default.
func ClampTimeout(requestedSeconds int, spec CommandSpec) time.Duration {
	if requestedSeconds <= 0 {
		return spec.Default
	}
	timeout := time.Duration(requestedSeconds) * time.Second
	if timeout < MinTimeout {
		return MinTimeout
	}
	if timeout > MaxTimeout {
		return MaxTimeout
	}
	return timeout
}

package cloudexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentic-ai-backend/internal/jobspec"
)

// DockerConfig sets the limits of every job container.
type DockerConfig struct {
	Binary    string // docker CLI, "docker" by default
	Images    jobspec.Images
	Memory    string // e.g. "1g"; also used as the swap limit so swap adds nothing
	CPUs      string // e.g. "1"
	PidsLimit int
	TmpSize   string // size of the writable /tmp, e.g. "256m"
	MaxFile   int64  // largest single file a process may create, in bytes
	// RunAs is the non-root user:group the command runs as. It must be able to
	// read and write the workspace files directory.
	RunAs string
	// ExtraEnv is added to the environment of the docker CLI process (for example
	// DOCKER_CONTEXT). It is never passed into the container.
	ExtraEnv []string
}

func DefaultDockerConfig() DockerConfig {
	return DockerConfig{Binary: "docker", Images: jobspec.DefaultImages(), Memory: "1g", CPUs: "1", PidsLimit: 128, TmpSize: "256m", MaxFile: 100 << 20, RunAs: "65534:65534"}
}

// DockerExecutor runs a job in a throwaway container with no network, a read-only
// root filesystem, no capabilities, resource limits and only the workspace files
// directory mounted. Docker is one layer of isolation, not the only one: see
// docs/phase6-cloud-workspace.md for what a deployment still has to provide.
type DockerExecutor struct {
	Config DockerConfig
}

func NewDockerExecutor(config DockerConfig) *DockerExecutor { return &DockerExecutor{Config: config} }

func (e *DockerExecutor) Name() string { return "docker" }

var (
	containerName = regexp.MustCompile(`^cloudexec-[a-z0-9-]{1,40}$`)
	sizeValue     = regexp.MustCompile(`^[0-9]{1,6}[kmg]$`)
	cpuValue      = regexp.MustCompile(`^[0-9](\.[0-9]{1,2})?$`)
	runAsValue    = regexp.MustCompile(`^[0-9]{1,10}:[0-9]{1,10}$`)
)

// Args builds the docker command line. Every value is checked again here, so a
// bad configuration or request cannot add a flag or change a mount.
func (e *DockerExecutor) Args(request RunRequest) ([]string, error) {
	cfg := e.Config
	spec := request.Spec
	switch {
	case !cfg.Images.Approved(spec.Image):
		return nil, fmt.Errorf("image %q is not an approved image", spec.Image)
	case len(spec.Argv) == 0:
		return nil, errors.New("empty command")
	case !containerName.MatchString(request.Name):
		return nil, errors.New("invalid container name")
	case !sizeValue.MatchString(cfg.Memory) || !sizeValue.MatchString(cfg.TmpSize):
		return nil, errors.New("invalid memory or tmp size")
	case !cpuValue.MatchString(cfg.CPUs):
		return nil, errors.New("invalid CPU limit")
	case cfg.PidsLimit < 16 || cfg.PidsLimit > 4096:
		return nil, errors.New("invalid PID limit")
	case !runAsValue.MatchString(cfg.RunAs) || strings.HasPrefix(cfg.RunAs, "0:") || cfg.RunAs == "0:0":
		return nil, errors.New("the job must run as a non-root user")
	case cfg.MaxFile <= 0:
		return nil, errors.New("invalid file size limit")
	}
	source, err := filepath.Abs(request.FilesDir)
	if err != nil || strings.ContainsAny(source, ",\n\r\x00") {
		return nil, errors.New("invalid workspace directory")
	}
	if info, statErr := os.Stat(source); statErr != nil || !info.IsDir() {
		return nil, errors.New("workspace directory does not exist")
	}

	args := []string{
		"run", "--rm",
		"--name", request.Name,
		"--network", "none",
		"--user", cfg.RunAs,
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(cfg.PidsLimit),
		"--memory", cfg.Memory, "--memory-swap", cfg.Memory,
		"--cpus", cfg.CPUs,
		"--ulimit", "nofile=1024:1024",
		"--ulimit", fmt.Sprintf("fsize=%d:%d", cfg.MaxFile, cfg.MaxFile),
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=" + cfg.TmpSize,
		"--mount", "type=bind,source=" + source + ",target=/workspace",
		"--workdir", "/workspace",
	}
	for _, variable := range environmentFor(spec.Runtime, source) {
		args = append(args, "--env", variable)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Argv...)
	return args, nil
}

// environmentFor returns the few variables a toolchain needs to work inside a
// read-only container with a throwaway /tmp. No variable of the server is passed on.
func environmentFor(runtime, filesDir string) []string {
	common := []string{"HOME=/tmp", "CI=true"}
	switch runtime {
	case "go":
		flags := "-buildvcs=false"
		if info, err := os.Stat(filepath.Join(filesDir, "vendor")); err == nil && info.IsDir() {
			flags += " -mod=vendor"
		}
		return append(common, "GOCACHE=/tmp/go-build", "GOPATH=/tmp/gopath", "GOPROXY=off", "GOFLAGS="+flags, "CGO_ENABLED=0")
	case "node":
		return append(common, "npm_config_cache=/tmp/npm-cache", "npm_config_update_notifier=false", "npm_config_audit=false", "npm_config_fund=false")
	case "python":
		return append(common, "PYTHONDONTWRITEBYTECODE=1", "PIP_NO_INDEX=1", "PYTHONPYCACHEPREFIX=/tmp/pycache")
	}
	return common
}

// command builds a docker CLI invocation with a minimal environment: the PATH and
// what the CLI needs to find the daemon, plus ExtraEnv. The server's own
// environment (database password, API keys) is not handed to it.
func (e *DockerExecutor) command(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, e.Config.Binary, args...)
	command.Env = append([]string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "DOCKER_HOST=" + os.Getenv("DOCKER_HOST")}, e.Config.ExtraEnv...)
	return command
}

// Available checks that the Docker daemon answers and that every approved image is
// already present. Images are never pulled by a job: a job has no network.
func (e *DockerExecutor) Available(ctx context.Context) error {
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if output, err := e.command(check, "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		return fmt.Errorf("the Docker daemon is not reachable: %s", firstLine(string(output)))
	}
	var missing []string
	for _, image := range []string{e.Config.Images.Go, e.Config.Images.Node, e.Config.Images.Python} {
		if err := e.command(check, "image", "inspect", "--format", "{{.Id}}", image).Run(); err != nil {
			missing = append(missing, image)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("approved images are missing on this host (pull them first): %s", strings.Join(missing, ", "))
	}
	return nil
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		text = text[:index]
	}
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

// Run starts the container and waits for it. On timeout or cancellation the
// container is killed by name; the CLI process alone being killed would leave it running.
func (e *DockerExecutor) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	args, err := e.Args(request)
	if err != nil {
		return RunResult{}, err
	}
	timeout := request.Timeout
	if timeout <= 0 || timeout > jobspec.MaxTimeout {
		timeout = jobspec.MaxTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr limitedBuffer
	command := e.command(runCtx, args...)
	command.Stdout, command.Stderr = &stdout, &stderr
	command.Cancel = func() error {
		e.kill(request.Name)
		return command.Process.Kill()
	}
	command.WaitDelay = 10 * time.Second

	runErr := command.Run()
	result := RunResult{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.Truncated() || stderr.Truncated()}
	switch {
	case ctx.Err() != nil:
		result.Cancelled = true
		e.kill(request.Name)
		return result, nil
	case runCtx.Err() != nil:
		result.TimedOut = true
		e.kill(request.Name)
		return result, nil
	}
	var exitError *exec.ExitError
	switch {
	case runErr == nil:
		return result, nil
	case errors.As(runErr, &exitError):
		result.ExitCode = exitError.ExitCode()
		if result.ExitCode == 125 { // docker itself failed, not the command
			return result, fmt.Errorf("the sandbox could not start the container: %s", firstLine(result.Stderr))
		}
		return result, nil
	default:
		return result, fmt.Errorf("the sandbox could not be started: %w", runErr)
	}
}

// kill stops and removes a container by name; it is safe to call when it is gone.
func (e *DockerExecutor) kill(name string) {
	if !containerName.MatchString(name) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = e.command(ctx, "kill", name).Run()
	_ = e.command(ctx, "rm", "-f", name).Run()
}

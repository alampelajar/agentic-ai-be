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
	// InstallNetwork and InstallProxy enable dependency installation, which is the
	// only kind of job that gets a network. InstallNetwork names a Docker network
	// created with --internal (no route to the host or the internet) and
	// InstallProxy is the address, on that network, of the registry proxy
	// (cmd/registry-proxy), the only peer the job can reach. Both empty means
	// install jobs are refused. See docs/phase6-cloud-workspace.md.
	InstallNetwork string
	InstallProxy   string
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
	networkName   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
	proxyAddress  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}:[0-9]{2,5}$`)
)

// ErrInstallNotConfigured is returned for an install job when the server has no
// registry-only network set up. The worker reports it as a sandbox failure.
var ErrInstallNotConfigured = errors.New("dependency installation is not set up on this server: the registry network is not configured")

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
	network := "none"
	if spec.Install {
		switch {
		case cfg.InstallNetwork == "" || cfg.InstallProxy == "":
			return nil, ErrInstallNotConfigured
		case !networkName.MatchString(cfg.InstallNetwork) || cfg.InstallNetwork == "host" || cfg.InstallNetwork == "bridge" || cfg.InstallNetwork == "none":
			return nil, errors.New("invalid install network")
		case !proxyAddress.MatchString(cfg.InstallProxy):
			return nil, errors.New("invalid registry proxy address")
		}
		network = cfg.InstallNetwork
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
		"--network", network,
		"--user", cfg.RunAs,
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(cfg.PidsLimit),
		"--memory", cfg.Memory, "--memory-swap", cfg.Memory,
		"--cpus", cfg.CPUs,
		"--ulimit", "nofile=1024:1024",
		"--ulimit", fmt.Sprintf("fsize=%d:%d", cfg.MaxFile, cfg.MaxFile),
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=" + cfg.TmpSize,
	}
	if spec.Runtime == "go" {
		// Docker mounts a tmpfs noexec, and `go test` runs the binary it just built.
		// Only Go gets a second scratch area that allows execution; /tmp stays noexec.
		args = append(args, "--tmpfs", goScratch+":rw,exec,nosuid,nodev,size="+cfg.TmpSize)
	}
	args = append(args,
		"--mount", "type=bind,source="+source+",target=/workspace",
		"--workdir", "/workspace",
	)
	for _, variable := range environmentFor(spec, source, cfg.InstallProxy) {
		args = append(args, "--env", variable)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Argv...)
	return args, nil
}

// goScratch is the writable, executable scratch area of Go jobs.
const goScratch = "/gotmp"

// environmentFor returns the few variables a toolchain needs to work inside a
// read-only container with a throwaway /tmp. No variable of the server is passed on.
// A test or build job gets none of the package manager settings that would let it
// fetch anything; an install job is pointed at the registries, through the proxy.
func environmentFor(spec jobspec.CommandSpec, filesDir, proxy string) []string {
	env := []string{"HOME=/tmp", "CI=true"}
	if spec.Install {
		address := "http://" + proxy
		env = append(env, "HTTP_PROXY="+address, "HTTPS_PROXY="+address, "http_proxy="+address, "https_proxy="+address)
	}
	switch spec.Runtime {
	case "go":
		flags := "-buildvcs=false"
		proxySetting := "GOPROXY=off"
		if spec.Install {
			proxySetting = "GOPROXY=https://proxy.golang.org"
		} else if info, err := os.Stat(filepath.Join(filesDir, "vendor")); err == nil && info.IsDir() {
			flags += " -mod=vendor"
		}
		// GOTOOLCHAIN=local: a go.mod that wants a newer Go fails clearly instead of
		// downloading a compiler.
		return append(env, "GOTMPDIR="+goScratch, "GOCACHE="+goScratch+"/go-build", "GOPATH="+goScratch+"/gopath", proxySetting, "GOFLAGS="+flags, "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	case "node":
		return append(env, "npm_config_cache=/tmp/npm-cache", "npm_config_update_notifier=false", "npm_config_audit=false", "npm_config_fund=false")
	case "python":
		env = append(env, "PYTHONDONTWRITEBYTECODE=1", "PYTHONPYCACHEPREFIX=/tmp/pycache", "PYTHONPATH=/workspace/"+jobspec.DependencyDir)
		if !spec.Install {
			env = append(env, "PIP_NO_INDEX=1")
		}
		return env
	}
	return env
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

// InstallReady checks the pieces an install job depends on: the network exists and
// is internal (so the job has no route to the host or the internet) and the
// registry proxy is running. A network that is not internal would give the job
// full internet access, so that is refused rather than assumed.
func (e *DockerExecutor) InstallReady(ctx context.Context) error {
	cfg := e.Config
	if cfg.InstallNetwork == "" || cfg.InstallProxy == "" {
		return ErrInstallNotConfigured
	}
	if !networkName.MatchString(cfg.InstallNetwork) || !proxyAddress.MatchString(cfg.InstallProxy) {
		return errors.New("invalid install network settings")
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := e.command(check, "network", "inspect", "--format", "{{.Internal}}", cfg.InstallNetwork).CombinedOutput()
	if err != nil {
		return fmt.Errorf("the install network %q does not exist (run deploy/registry-proxy/setup.sh)", cfg.InstallNetwork)
	}
	if strings.TrimSpace(string(output)) != "true" {
		return fmt.Errorf("the install network %q is not an internal network, so it would give jobs internet access", cfg.InstallNetwork)
	}
	proxyName, _, _ := strings.Cut(cfg.InstallProxy, ":")
	output, err = e.command(check, "inspect", "--format", "{{.State.Running}}", proxyName).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "true" {
		return fmt.Errorf("the registry proxy %q is not running (run deploy/registry-proxy/setup.sh)", proxyName)
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
// A spec with Then commands runs them one after another, each in a fresh container,
// inside the one timeout, and stops at the first failure.
func (e *DockerExecutor) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	commands := append([][]string{request.Spec.Argv}, request.Spec.Then...)
	allArgs := make([][]string, len(commands))
	for index, argv := range commands {
		step := request
		step.Spec.Argv = argv
		args, err := e.Args(step) // every step is checked before the first one starts
		if err != nil {
			return RunResult{}, err
		}
		allArgs[index] = args
	}
	if request.Spec.Install {
		if err := e.InstallReady(ctx); err != nil {
			return RunResult{}, err
		}
	}
	timeout := request.Timeout
	if timeout <= 0 || timeout > jobspec.MaxTimeout {
		timeout = jobspec.MaxTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr limitedBuffer
	result := RunResult{}
	finish := func() RunResult {
		result.Stdout, result.Stderr, result.Truncated = stdout.String(), stderr.String(), stdout.Truncated() || stderr.Truncated()
		return result
	}
	for index, args := range allArgs {
		if len(allArgs) > 1 {
			fmt.Fprintf(&stdout, "$ %s\n", strings.Join(commands[index], " "))
		}
		command := e.command(runCtx, args...)
		command.Stdout, command.Stderr = &stdout, &stderr
		command.Cancel = func() error {
			e.kill(request.Name)
			return command.Process.Kill()
		}
		command.WaitDelay = 10 * time.Second

		runErr := command.Run()
		switch {
		case ctx.Err() != nil:
			result.Cancelled = true
			e.kill(request.Name)
			return finish(), nil
		case runCtx.Err() != nil:
			result.TimedOut = true
			e.kill(request.Name)
			return finish(), nil
		}
		var exitError *exec.ExitError
		switch {
		case runErr == nil:
			continue
		case errors.As(runErr, &exitError):
			result.ExitCode = exitError.ExitCode()
			if result.ExitCode == 125 { // docker itself failed, not the command
				finish()
				return result, fmt.Errorf("the sandbox could not start the container: %s", firstLine(result.Stderr))
			}
			return finish(), nil
		default:
			finish()
			return result, fmt.Errorf("the sandbox could not be started: %w", runErr)
		}
	}
	return finish(), nil
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

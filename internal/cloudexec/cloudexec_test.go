package cloudexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentic-ai-backend/internal/jobspec"
)

// TestMain lets the test binary stand in for the docker CLI: when it is started
// with GO_FAKE_DOCKER set it behaves like docker instead of running tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("GO_FAKE_DOCKER"); mode != "" {
		os.Exit(fakeDocker(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeDocker(mode string, args []string) int {
	log := os.Getenv("GO_FAKE_DOCKER_LOG")
	record := func(line string) {
		if log == "" {
			return
		}
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		defer f.Close()
		fmt.Fprintln(f, line)
	}
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "kill", "rm":
		record(strings.Join(args, " "))
		return 0
	case "version":
		if mode == "daemon-down" {
			fmt.Fprintln(os.Stderr, "Cannot connect to the Docker daemon")
			return 1
		}
		fmt.Println("27.0.0")
		return 0
	case "image":
		if mode == "missing-image" {
			return 1
		}
		return 0
	case "run":
		record("run " + strings.Join(args[1:], " "))
		switch mode {
		case "ok":
			fmt.Println("PASS ok example/app 0.1s")
			fmt.Fprintln(os.Stderr, "warning: something")
			return 0
		case "fail3":
			fmt.Println("--- FAIL: TestSomething")
			return 3
		case "secret":
			fmt.Println("DATABASE token=abc123secret and Bearer abcdefghijklmnop1234")
			return 0
		case "sleep":
			time.Sleep(30 * time.Second)
			return 0
		case "flood":
			chunk := strings.Repeat("x", 1024)
			for i := 0; i < 400; i++ {
				fmt.Print(chunk)
			}
			return 0
		case "docker125":
			fmt.Fprintln(os.Stderr, "docker: Error response from daemon: no space left on device")
			return 125
		}
	}
	return 2
}

func fakeExecutor(t *testing.T, mode string) (*DockerExecutor, string) {
	t.Helper()
	t.Setenv("GO_FAKE_DOCKER", mode) // the fake runs as a child of this process
	logFile := filepath.Join(t.TempDir(), "docker.log")
	config := DefaultDockerConfig()
	config.Binary = os.Args[0]
	config.ExtraEnv = []string{"GO_FAKE_DOCKER=" + mode, "GO_FAKE_DOCKER_LOG=" + logFile}
	return NewDockerExecutor(config), logFile
}

func runRequest(t *testing.T, timeout time.Duration) RunRequest {
	t.Helper()
	dir := t.TempDir()
	spec, err := jobspec.Resolve("go-test", jobspec.DefaultImages(), "")
	if err != nil {
		t.Fatal(err)
	}
	return RunRequest{Name: "cloudexec-7-abc", FilesDir: dir, Spec: spec, Timeout: timeout}
}

func readLog(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func TestDockerArgsApplyEveryIsolationSetting(t *testing.T) {
	executor := NewDockerExecutor(DefaultDockerConfig())
	request := runRequest(t, time.Minute)
	args, err := executor.Args(request)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, required := range []string{
		"run --rm", "--network none", "--user 65534:65534", "--read-only", "--cap-drop ALL",
		"--security-opt no-new-privileges", "--pids-limit 128", "--memory 1g", "--memory-swap 1g", "--cpus 1",
		"--ulimit nofile=1024:1024", "--ulimit fsize=104857600:104857600", "--tmpfs /tmp:rw,nosuid,nodev,size=256m",
		"--workdir /workspace", "--name cloudexec-7-abc",
	} {
		if !strings.Contains(line, required) {
			t.Errorf("missing %q in %s", required, line)
		}
	}
	// The image and the command come last, as a fixed argument array.
	if tail := strings.Join(args[len(args)-4:], " "); tail != "golang:1.27 go test ./..." {
		t.Errorf("image and command = %q", tail)
	}
	// Only the workspace files directory is mounted.
	mounts := 0
	for index, arg := range args {
		if arg == "--mount" {
			mounts++
			if !strings.HasPrefix(args[index+1], "type=bind,source="+request.FilesDir) || !strings.HasSuffix(args[index+1], ",target=/workspace") || strings.Contains(args[index+1], "readonly") == true {
				t.Errorf("mount = %s", args[index+1])
			}
		}
	}
	if mounts != 1 {
		t.Errorf("%d mounts, want exactly 1", mounts)
	}
}

func TestDockerArgsNeverContainDangerousOptions(t *testing.T) {
	executor := NewDockerExecutor(DefaultDockerConfig())
	for _, jobType := range jobspec.JobTypes() {
		dir := t.TempDir()
		for _, manifest := range []string{"go.mod", "package.json", "pyproject.toml"} {
			_ = os.WriteFile(filepath.Join(dir, manifest), []byte("x"), 0o600)
		}
		spec, err := jobspec.Resolve(jobType, jobspec.DefaultImages(), dir)
		if err != nil {
			t.Fatalf("%s: %v", jobType, err)
		}
		args, err := executor.Args(RunRequest{Name: "cloudexec-1-a", FilesDir: dir, Spec: spec})
		if err != nil {
			t.Fatalf("%s: %v", jobType, err)
		}
		line := strings.Join(args, " ")
		// Compare whole arguments: "--pid" must not match "--pids-limit".
		forbidden := []string{"--privileged", "--pid", "--ipc", "--uts", "--cap-add", "--device", "--volumes-from", "--userns", "--env-file", "--add-host", "-p", "--publish", "-v", "--volume"}
		for index, arg := range args {
			for _, banned := range forbidden {
				if arg == banned || strings.HasPrefix(arg, banned+"=") {
					t.Errorf("%s: option %q in %s", jobType, arg, line)
				}
			}
			if arg == "--network" && args[index+1] != "none" {
				t.Errorf("%s: network %q", jobType, args[index+1])
			}
			if arg == "--security-opt" && args[index+1] != "no-new-privileges" {
				t.Errorf("%s: security option %q", jobType, args[index+1])
			}
			if arg == "--user" && (strings.HasPrefix(args[index+1], "0") || args[index+1] == "root") {
				t.Errorf("%s: user %q", jobType, args[index+1])
			}
		}
		for _, banned := range []string{"docker.sock", "/var/run", "seccomp=unconfined", "apparmor=unconfined"} {
			if strings.Contains(line, banned) {
				t.Errorf("%s: %q in %s", jobType, banned, line)
			}
		}
		if os.Getenv("DB_PASSWORD") != "" && strings.Contains(line, os.Getenv("DB_PASSWORD")) {
			t.Errorf("%s: a server secret reached the container command line", jobType)
		}
		for index, arg := range args {
			if arg == "--env" && (strings.HasPrefix(args[index+1], "DB_") || strings.Contains(args[index+1], "KEY") || strings.Contains(args[index+1], "TOKEN") || strings.Contains(args[index+1], "SECRET")) {
				t.Errorf("%s: suspicious environment variable %s", jobType, args[index+1])
			}
		}
	}
}

func TestDockerArgsRefuseBadInput(t *testing.T) {
	good := runRequest(t, time.Minute)
	cases := map[string]func(*DockerExecutor, *RunRequest){
		"image not approved": func(_ *DockerExecutor, r *RunRequest) { r.Spec.Image = "alpine:latest" },
		"empty image":        func(_ *DockerExecutor, r *RunRequest) { r.Spec.Image = "" },
		"image injection":    func(_ *DockerExecutor, r *RunRequest) { r.Spec.Image = "golang:1.27 --privileged" },
		"no command":         func(_ *DockerExecutor, r *RunRequest) { r.Spec.Argv = nil },
		"name injection":     func(_ *DockerExecutor, r *RunRequest) { r.Name = "x --privileged" },
		"name with slash":    func(_ *DockerExecutor, r *RunRequest) { r.Name = "cloudexec-../x" },
		"missing directory":  func(_ *DockerExecutor, r *RunRequest) { r.FilesDir = filepath.Join(r.FilesDir, "missing") },
		"comma in directory": func(_ *DockerExecutor, r *RunRequest) { r.FilesDir = r.FilesDir + ",readonly=false" },
		"root user":          func(e *DockerExecutor, _ *RunRequest) { e.Config.RunAs = "0:0" },
		"root group only":    func(e *DockerExecutor, _ *RunRequest) { e.Config.RunAs = "0:1000" },
		"user name":          func(e *DockerExecutor, _ *RunRequest) { e.Config.RunAs = "root" },
		"memory injection":   func(e *DockerExecutor, _ *RunRequest) { e.Config.Memory = "1g --privileged" },
		"cpu injection":      func(e *DockerExecutor, _ *RunRequest) { e.Config.CPUs = "1 --privileged" },
		"tiny pid limit":     func(e *DockerExecutor, _ *RunRequest) { e.Config.PidsLimit = 1 },
		"unlimited pids":     func(e *DockerExecutor, _ *RunRequest) { e.Config.PidsLimit = 0 },
		"no file size limit": func(e *DockerExecutor, _ *RunRequest) { e.Config.MaxFile = 0 },
		"tmp size injection": func(e *DockerExecutor, _ *RunRequest) { e.Config.TmpSize = "256m,exec" },
	}
	for name, mutate := range cases {
		executor := NewDockerExecutor(DefaultDockerConfig())
		request := good
		mutate(executor, &request)
		if _, err := executor.Args(request); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGoToolchainUsesVendorOnlyWhenPresent(t *testing.T) {
	dir := t.TempDir()
	if env := strings.Join(environmentFor("go", dir), " "); strings.Contains(env, "-mod=vendor") || !strings.Contains(env, "GOPROXY=off") {
		t.Errorf("env without vendor = %s", env)
	}
	_ = os.Mkdir(filepath.Join(dir, "vendor"), 0o750)
	if env := strings.Join(environmentFor("go", dir), " "); !strings.Contains(env, "-mod=vendor") {
		t.Errorf("env with vendor = %s", env)
	}
}

func TestJobSpecIsAnAllowlist(t *testing.T) {
	for _, bad := range []string{"", "bash", "sh -c id", "go-test; rm -rf /", "go-test && curl evil.sh", "go-test\nid", "powershell", "cmd", "docker-run", "curl", "wget", "GO-TEST", "go-test ", "../go-test", "npm-install", "pip-install", "git-clone"} {
		if _, err := jobspec.Resolve(bad, jobspec.DefaultImages(), ""); err == nil {
			t.Errorf("%q was accepted as a job type", bad)
		}
	}
	for _, jobType := range jobspec.JobTypes() {
		spec, err := jobspec.Resolve(jobType, jobspec.DefaultImages(), "")
		if err != nil {
			t.Fatalf("%s: %v", jobType, err)
		}
		for _, arg := range spec.Argv {
			if strings.ContainsAny(arg, " ;&|$`\n><") {
				t.Errorf("%s: argument %q looks like shell text", jobType, arg)
			}
		}
		if spec.Argv[0] == "sh" || spec.Argv[0] == "bash" || spec.Argv[0] == "cmd" {
			t.Errorf("%s runs a shell", jobType)
		}
	}
	dir := t.TempDir()
	for jobType, want := range map[string]string{"go-test": "go.mod", "go-build": "go.mod", "npm-test": "package.json", "npm-build": "package.json", "npm-lint": "package.json", "python-test": "pytest"} {
		_, err := jobspec.Resolve(jobType, jobspec.DefaultImages(), dir)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s in an empty project = %v", jobType, err)
		}
	}
}

func TestClampTimeout(t *testing.T) {
	spec, _ := jobspec.Resolve("go-test", jobspec.DefaultImages(), "")
	for seconds, want := range map[int]time.Duration{0: spec.Default, -5: spec.Default, 1: jobspec.MinTimeout, 60: time.Minute, 999999: jobspec.MaxTimeout} {
		if got := jobspec.ClampTimeout(seconds, spec); got != want {
			t.Errorf("ClampTimeout(%d) = %s, want %s", seconds, got, want)
		}
	}
}

func TestLimitedBufferKeepsTheStartAndCounts(t *testing.T) {
	var buffer limitedBuffer
	chunk := strings.Repeat("a", 1000)
	for i := 0; i < 100; i++ {
		if n, err := buffer.Write([]byte(chunk)); n != 1000 || err != nil {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if len(buffer.String()) != MaxOutputBytes || !buffer.Truncated() {
		t.Fatalf("kept %d bytes, truncated=%v", len(buffer.String()), buffer.Truncated())
	}
}

func TestDockerRunCapturesOutputAndExitCode(t *testing.T) {
	executor, logFile := fakeExecutor(t, "ok")
	result, err := executor.Run(context.Background(), runRequest(t, time.Minute))
	if err != nil || result.ExitCode != 0 || result.TimedOut || result.Cancelled {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if !strings.Contains(result.Stdout, "PASS") || !strings.Contains(result.Stderr, "warning") {
		t.Fatalf("output = %q / %q", result.Stdout, result.Stderr)
	}
	if !strings.Contains(readLog(logFile), "run --rm --name cloudexec-7-abc --network none") {
		t.Fatalf("docker was called with: %s", readLog(logFile))
	}

	executor, _ = fakeExecutor(t, "fail3")
	result, err = executor.Run(context.Background(), runRequest(t, time.Minute))
	if err != nil || result.ExitCode != 3 {
		t.Fatalf("a failing command = %+v, %v", result, err)
	}

	executor, _ = fakeExecutor(t, "docker125")
	if _, err = executor.Run(context.Background(), runRequest(t, time.Minute)); err == nil || !strings.Contains(err.Error(), "could not start") {
		t.Fatalf("a sandbox failure must be an error, got %v", err)
	}
}

func TestDockerRunBoundsHugeOutput(t *testing.T) {
	executor, _ := fakeExecutor(t, "flood")
	result, err := executor.Run(context.Background(), runRequest(t, time.Minute))
	if err != nil || len(result.Stdout) != MaxOutputBytes || !result.Truncated {
		t.Fatalf("flood: %d bytes, truncated=%v, %v", len(result.Stdout), result.Truncated, err)
	}
}

func TestDockerRunStopsAtTheTimeout(t *testing.T) {
	executor, logFile := fakeExecutor(t, "sleep")
	started := time.Now()
	result, err := executor.Run(context.Background(), runRequest(t, 1*time.Second))
	if err != nil || !result.TimedOut || result.Cancelled {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 12*time.Second {
		t.Fatalf("the command was not stopped at the timeout (%s)", elapsed)
	}
	log := readLog(logFile)
	if !strings.Contains(log, "kill cloudexec-7-abc") || !strings.Contains(log, "rm -f cloudexec-7-abc") {
		t.Fatalf("the container was not killed and removed: %s", log)
	}
}

func TestDockerRunStopsWhenCancelled(t *testing.T) {
	executor, logFile := fakeExecutor(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	result, err := executor.Run(ctx, runRequest(t, time.Minute))
	if err != nil || !result.Cancelled || result.TimedOut {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 12*time.Second {
		t.Fatalf("the command was not stopped after the cancel (%s)", elapsed)
	}
	if !strings.Contains(readLog(logFile), "kill cloudexec-7-abc") {
		t.Fatalf("the container was not killed: %s", readLog(logFile))
	}
}

func TestDockerAvailability(t *testing.T) {
	executor, _ := fakeExecutor(t, "ok")
	if err := executor.Available(context.Background()); err != nil {
		t.Fatalf("a working sandbox: %v", err)
	}
	executor, _ = fakeExecutor(t, "daemon-down")
	if err := executor.Available(context.Background()); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("daemon down = %v", err)
	}
	executor, _ = fakeExecutor(t, "missing-image")
	if err := executor.Available(context.Background()); err == nil || !strings.Contains(err.Error(), "images are missing") {
		t.Fatalf("missing images = %v", err)
	}
	if err := (DisabledExecutor{}).Available(context.Background()); err == nil {
		t.Fatal("the disabled executor claims to be available")
	}
	if _, err := (DisabledExecutor{}).Run(context.Background(), RunRequest{}); err == nil {
		t.Fatal("the disabled executor ran something")
	}
}

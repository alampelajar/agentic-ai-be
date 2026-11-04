package cloudexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentic-ai-backend/internal/jobspec"
)

// These tests run real containers. They are skipped unless CLOUDEXEC_DOCKER_TESTS=1,
// the Docker daemon answers and the approved images are already pulled
// (docker pull golang:1.27). They are the evidence that the sandbox does what
// its arguments claim.

func realExecutor(t *testing.T) *DockerExecutor {
	t.Helper()
	if os.Getenv("CLOUDEXEC_DOCKER_TESTS") != "1" {
		t.Skip("set CLOUDEXEC_DOCKER_TESTS=1 to run the real Docker tests")
	}
	executor := NewDockerExecutor(DefaultDockerConfig())
	if err := executor.Available(context.Background()); err != nil {
		t.Skipf("the sandbox is not available: %v", err)
	}
	return executor
}

// goProject writes a tiny Go module with the given test body into a new folder.
func goProject(t *testing.T, testBody string, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example\n\ngo 1.21\n",
		"main.go":      "package main\n\nfunc main() {}\n",
		"main_test.go": "package main\n\nimport (\n\t\"testing\"\n\t\"os\"\n\t\"os/exec\"\n\t\"net\"\n\t\"time\"\n\t\"strings\"\n\t\"fmt\"\n)\n\nvar _ = os.Getuid\nvar _ = exec.Command\nvar _ = net.Dial\nvar _ = time.Now\nvar _ = strings.Contains\nvar _ = fmt.Sprint\n\n" + testBody,
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runGo(t *testing.T, executor *DockerExecutor, dir, jobType string, timeout time.Duration) RunResult {
	t.Helper()
	spec, err := jobspec.Resolve(jobType, jobspec.DefaultImages(), dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Run(context.Background(), RunRequest{Name: "cloudexec-t-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))[:min(30, len(t.Name()))], FilesDir: dir, Spec: spec, Timeout: timeout})
	if err != nil {
		t.Fatalf("run failed: %v (stderr: %s)", err, result.Stderr)
	}
	return result
}

func containerExists(name string) bool {
	return exec.Command("docker", "inspect", name).Run() == nil
}

func TestRealGoTestPassesAndFails(t *testing.T) {
	executor := realExecutor(t)
	pass := runGo(t, executor, goProject(t, "func TestOK(t *testing.T) {}\n", nil), "go-test", 2*time.Minute)
	if pass.ExitCode != 0 || !strings.Contains(pass.Stdout, "ok") {
		t.Fatalf("passing project: exit %d\n%s\n%s", pass.ExitCode, pass.Stdout, pass.Stderr)
	}
	fail := runGo(t, executor, goProject(t, "func TestBad(t *testing.T) { t.Fatal(\"broken on purpose\") }\n", nil), "go-test", 2*time.Minute)
	if fail.ExitCode == 0 || !strings.Contains(fail.Stdout+fail.Stderr, "broken on purpose") {
		t.Fatalf("failing project: exit %d\n%s\n%s", fail.ExitCode, fail.Stdout, fail.Stderr)
	}
	build := runGo(t, executor, goProject(t, "func TestOK(t *testing.T) {}\n", map[string]string{"main.go": "package main\n\nfunc main() { undefined() }\n"}), "go-build", 2*time.Minute)
	if build.ExitCode == 0 || !strings.Contains(build.Stdout+build.Stderr, "undefined") {
		t.Fatalf("broken build: exit %d\n%s\n%s", build.ExitCode, build.Stdout, build.Stderr)
	}
}

// The probe runs inside the container as a test and fails on any weakness.
func TestRealSandboxIsolation(t *testing.T) {
	executor := realExecutor(t)
	t.Setenv("CLOUDEXEC_SECRET_PROBE", "must-not-reach-the-container")
	t.Setenv("DB_PASSWORD", "must-not-reach-the-container")
	probe := `
func status(t *testing.T, key string) string {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil { t.Fatal(err) }
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, key+":") { return strings.TrimSpace(strings.TrimPrefix(line, key+":")) }
	}
	return ""
}

func TestSandbox(t *testing.T) {
	if os.Getuid() == 0 { t.Error("running as root") }
	if got := status(t, "CapEff"); strings.Trim(got, "0") != "" { t.Errorf("capabilities left: %s", got) }
	if got := status(t, "NoNewPrivs"); got != "1" { t.Errorf("no_new_privs = %q", got) }
	for _, forbidden := range []string{"CLOUDEXEC_SECRET_PROBE", "DB_PASSWORD", "JWT_SECRET", "AI_ENCRYPTION_KEY"} {
		if _, found := os.LookupEnv(forbidden); found { t.Errorf("server variable %s reached the container", forbidden) }
	}
	for _, path := range []string{"/var/run/docker.sock", "/run/docker.sock", "/host", "/mnt/c", "/c"} {
		if _, err := os.Stat(path); err == nil { t.Errorf("%s is visible", path) }
	}
	for _, path := range []string{"/etc/cloudexec-probe", "/usr/cloudexec-probe", "/cloudexec-probe", "/root/x"} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err == nil { t.Errorf("could write %s: the root filesystem is not read-only", path) }
	}
	if err := os.WriteFile("/tmp/cloudexec-probe", []byte("x"), 0o600); err != nil { t.Errorf("cannot use /tmp: %v", err) }
	if err := os.WriteFile("/workspace/written-by-sandbox.txt", []byte("ok"), 0o600); err != nil { t.Errorf("cannot write the workspace: %v", err) }
	conn, err := net.DialTimeout("tcp", "1.1.1.1:53", 3*time.Second)
	if err == nil { conn.Close(); t.Error("the network is reachable") }
	if addrs, err := net.LookupHost("example.com"); err == nil { t.Errorf("DNS works: %v", addrs) }
}

func TestProcessLimit(t *testing.T) {
	started := 0
	var children []*exec.Cmd
	for i := 0; i < 400; i++ {
		cmd := exec.Command("sleep", "20")
		if err := cmd.Start(); err != nil { break }
		children = append(children, cmd)
		started++
	}
	for _, cmd := range children { _ = cmd.Process.Kill() }
	if started >= 300 { t.Errorf("started %d processes: the PID limit is not applied", started) }
	t.Logf("started %d processes before the limit", started)
}
`
	dir := goProject(t, probe, nil)
	result := runGo(t, executor, dir, "go-test", 3*time.Minute)
	if result.ExitCode != 0 {
		t.Fatalf("the sandbox probe failed (exit %d):\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	// The one thing the job may do is leave files in the workspace folder.
	if _, err := os.Stat(filepath.Join(dir, "written-by-sandbox.txt")); err != nil {
		t.Errorf("the job could not write into the workspace: %v", err)
	}
}

func TestRealTimeoutKillsTheContainer(t *testing.T) {
	executor := realExecutor(t)
	dir := goProject(t, "func TestSlow(t *testing.T) { time.Sleep(10 * time.Minute) }\n", nil)
	started := time.Now()
	result := runGo(t, executor, dir, "go-test", 25*time.Second)
	if !result.TimedOut || result.Cancelled {
		t.Fatalf("result = %+v", result)
	}
	if elapsed := time.Since(started); elapsed > 60*time.Second {
		t.Fatalf("took %s to stop", elapsed)
	}
	time.Sleep(2 * time.Second)
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name=cloudexec-t-").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("a container is left behind: %s", out)
	}
}

func TestRealCancelKillsTheContainer(t *testing.T) {
	executor := realExecutor(t)
	dir := goProject(t, "func TestSlow(t *testing.T) { time.Sleep(10 * time.Minute) }\n", nil)
	spec, _ := jobspec.Resolve("go-test", jobspec.DefaultImages(), dir)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Second) // after the compile, while the test sleeps
		cancel()
	}()
	started := time.Now()
	result, err := executor.Run(ctx, RunRequest{Name: "cloudexec-t-cancel", FilesDir: dir, Spec: spec, Timeout: 5 * time.Minute})
	if err != nil || !result.Cancelled || result.TimedOut {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if time.Since(started) > 60*time.Second {
		t.Fatal("cancellation was slow")
	}
	time.Sleep(2 * time.Second)
	if containerExists("cloudexec-t-cancel") {
		t.Fatal("the container is still there after cancel")
	}
}

func TestRealMemoryLimit(t *testing.T) {
	executor := realExecutor(t)
	executor.Config.Memory = "256m"
	dir := goProject(t, "func TestHog(t *testing.T) { var keep [][]byte; for i := 0; i < 64; i++ { b := make([]byte, 32<<20); for j := range b { b[j] = 1 }; keep = append(keep, b) }; t.Log(len(keep)) }\n", nil)
	result := runGo(t, executor, dir, "go-test", 3*time.Minute)
	if result.ExitCode == 0 {
		t.Fatalf("a test that needs 2 GiB passed under a 256 MiB limit:\n%s", result.Stdout)
	}
}

func TestRealOutputIsBounded(t *testing.T) {
	executor := realExecutor(t)
	dir := goProject(t, "func TestLoud(t *testing.T) { s := strings.Repeat(\"x\", 1024); for i := 0; i < 2000; i++ { fmt.Println(s) }; t.Fatal(\"done\") }\n", nil)
	result := runGo(t, executor, dir, "go-test", 3*time.Minute)
	if len(result.Stdout) > MaxOutputBytes || !result.Truncated {
		t.Fatalf("stdout %d bytes, truncated=%v", len(result.Stdout), result.Truncated)
	}
}

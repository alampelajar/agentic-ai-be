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
	"agentic-ai-backend/models"
)

// The whole path with nothing stubbed: a job row in PostgreSQL, the real worker,
// the real Docker sandbox, the result written back to the row. Needs both
// SKILLS_TEST_DATABASE_URL and CLOUDEXEC_DOCKER_TESTS=1.

func realFixture(t *testing.T, files map[string]string) *fixture {
	t.Helper()
	executor := realExecutor(t)
	f := newFixture(t)
	f.worker.Executor = executor
	f.worker.Images = jobspec.DefaultImages()
	dir, err := f.storage.FilesDir(f.owner.ID, f.ws.StorageRef)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// leftoverJobContainers lists the job containers that exist (running or not). The
// registry proxy has the same name prefix but is infrastructure, not a job.
func leftoverJobContainers() string {
	out, _ := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}", "--filter", "name=cloudexec-").Output()
	var jobs []string
	for _, name := range strings.Fields(string(out)) {
		if name != "cloudexec-registry-proxy" {
			jobs = append(jobs, name)
		}
	}
	return strings.Join(jobs, " ")
}

const passingTest = "package main\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n"

func TestRealWorkerRunsAJobFromQueueToResult(t *testing.T) {
	f := realFixture(t, map[string]string{
		"go.mod": "module example\n\ngo 1.21\n", "main.go": "package main\n\nfunc main() {}\n", "main_test.go": passingTest,
	})
	job := f.queue(t, f.owner.ID, "go-test")
	done := f.claimAndRun(t, job)
	if done.Status != models.JobCompleted || done.ExitCode == nil || *done.ExitCode != 0 || !strings.Contains(done.Stdout, "ok") {
		t.Fatalf("job = %+v", done)
	}
	if done.StartedAt == nil || done.CompletedAt == nil {
		t.Fatalf("times = %v %v", done.StartedAt, done.CompletedAt)
	}

	// The same project with a failing test ends as failed, with the reason in the output.
	dir, _ := f.storage.FilesDir(f.owner.ID, f.ws.StorageRef)
	broken := strings.Replace(passingTest, "func TestOK(t *testing.T) {}", "func TestOK(t *testing.T) { t.Fatal(\"broken on purpose\") }", 1)
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(broken), 0o640); err != nil {
		t.Fatal(err)
	}
	failed := f.claimAndRun(t, f.queue(t, f.owner.ID, "go-test"))
	if failed.Status != models.JobFailed || failed.ExitCode == nil || *failed.ExitCode == 0 || !strings.Contains(failed.Stdout, "broken on purpose") {
		t.Fatalf("job = %+v", failed)
	}
}

func TestRealWorkerTimesOutAndCleansUp(t *testing.T) {
	f := realFixture(t, map[string]string{
		"go.mod": "module example\n\ngo 1.21\n", "main.go": "package main\n\nfunc main() {}\n",
		"main_test.go": "package main\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) { time.Sleep(10 * time.Minute) }\n",
	})
	// 10 seconds is the shortest allowed; the compile happens first, so allow for it.
	f.worker.PollInterval = 100 * time.Millisecond
	job := f.queue(t, f.owner.ID, "go-test")
	f.db.Model(job).Update("timeout_seconds", 40)
	started := time.Now()
	done := f.claimAndRun(t, job)
	if done.Status != models.JobTimedOut {
		t.Fatalf("job = %+v", done)
	}
	if elapsed := time.Since(started); elapsed > 90*time.Second {
		t.Fatalf("took %s", elapsed)
	}
	time.Sleep(2 * time.Second)
	if left := leftoverJobContainers(); left != "" {
		t.Fatalf("a container was left behind: %s", left)
	}
}

func TestRealWorkerStopsARunningJobOnCancel(t *testing.T) {
	f := realFixture(t, map[string]string{
		"go.mod": "module example\n\ngo 1.21\n", "main.go": "package main\n\nfunc main() {}\n",
		"main_test.go": "package main\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) { time.Sleep(10 * time.Minute) }\n",
	})
	job := f.queue(t, f.owner.ID, "go-test")
	f.db.Model(job).Update("timeout_seconds", 300)
	claimed, err := f.worker.ClaimNext(context.Background())
	if err != nil || claimed == nil {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	finished := make(chan struct{})
	go func() {
		f.worker.RunJob(context.Background(), claimed)
		close(finished)
	}()
	// Wait until the container really exists, then cancel the way the API does.
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if leftoverJobContainers() != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the container never started")
		}
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(15 * time.Second) // let the test binary start sleeping
	f.db.Model(&models.ExecutionJob{}).Where("id = ?", job.ID).UpdateColumn("cancel_requested", true)
	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatal("the running job was not stopped after a cancel request")
	}
	if done := f.reload(job); done.Status != models.JobCancelled {
		t.Fatalf("job = %+v", done)
	}
	time.Sleep(2 * time.Second)
	if left := leftoverJobContainers(); left != "" {
		t.Fatalf("a container was left behind: %s", left)
	}
}

func TestRealWorkerInstallsDependenciesThenTestsOffline(t *testing.T) {
	installer := installExecutor(t)
	f := newFixture(t)
	f.worker.Executor = installer
	f.worker.Images = jobspec.DefaultImages()
	dir, err := f.storage.FilesDir(f.owner.ID, f.ws.StorageRef)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"package.json": `{"name":"app","version":"1.0.0","scripts":{"test":"node check.js"},"dependencies":{"left-pad":"1.3.0"}}`,
		"check.js":     `if (require('left-pad')('7', 3, '0') !== '007') process.exit(1); console.log('deps ok')`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	queue := func(jobType, command, runtime string) models.ExecutionJob {
		job := &models.ExecutionJob{UserID: f.owner.ID, WorkspaceID: f.ws.ID, JobType: jobType, Command: command, Runtime: runtime, Status: models.JobQueued, TimeoutSeconds: 300}
		if err := f.db.Create(job).Error; err != nil {
			t.Fatal(err)
		}
		return f.claimAndRun(t, job)
	}

	install := queue("npm-install", "npm install", "node")
	if install.Status != models.JobCompleted || install.ExitCode == nil || *install.ExitCode != 0 {
		t.Fatalf("install = %+v", install)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "left-pad", "package.json")); err != nil {
		t.Fatalf("the dependency was not installed: %v", err)
	}
	test := queue("npm-test", "npm run test", "node")
	if test.Status != models.JobCompleted || !strings.Contains(test.Stdout, "deps ok") {
		t.Fatalf("test = %+v", test)
	}

	// The workspace's cached size covers the project, not the dependency folder.
	var refreshed models.CloudWorkspace
	f.db.First(&refreshed, f.ws.ID)
	if refreshed.SizeBytes > 4096 {
		t.Errorf("the dependency folder was counted as project size: %d bytes", refreshed.SizeBytes)
	}
}

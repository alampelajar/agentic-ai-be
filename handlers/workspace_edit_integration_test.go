package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditingChangesOnlyTheApprovedPart(t *testing.T) {
	f := newToolFixture(t)
	owner := f.owner.ID
	dir := filepath.Join(f.filesDir(t, f.ws), "site")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "<h1>Rina</h1>\r\n<p>Halo</p>\r\n<footer>2025</footer>\r\n"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// An edit that cannot apply is answered at once, without an approval.
	for oldText, want := range map[string]string{"<p>Selamat datang</p>": "was not found", "\r\n": "appears 3 times"} {
		status, response := f.run(t, owner, "coding", "workspace.edit_file", map[string]any{"path": "site/index.html", "old_text": oldText, "new_text": "x"})
		if status != http.StatusOK || response.ApprovalRequired || response.Result["ok"] != false || !strings.Contains(fmt.Sprint(response.Result["error"]), want) {
			t.Fatalf("edit %q = %d %+v", oldText, status, response)
		}
	}
	if code, _ := f.run(t, owner, "coding", "workspace.edit_file", map[string]any{"path": "site/index.html", "old_text": "", "new_text": "x"}); code != http.StatusBadRequest {
		t.Fatalf("an empty old_text must be refused, got %d", code)
	}
	if code, _ := f.run(t, owner, "review", "workspace.edit_file", map[string]any{"path": "site/index.html", "old_text": "Halo", "new_text": "x"}); code == http.StatusAccepted || code == http.StatusOK {
		t.Fatalf("a review agent must not edit, got %d", code)
	}

	// The model sends \n line breaks; the file has \r\n. The edit still matches.
	status, pending := f.run(t, owner, "coding", "workspace.edit_file", map[string]any{"path": "site/index.html", "old_text": "<p>Halo</p>\n<footer>2025", "new_text": "<p>Halo, saya Rina</p>\n<footer>2026"})
	if status != http.StatusAccepted || !pending.ApprovalRequired || !strings.Contains(pending.Approval.Summary, "Edit site/index.html") {
		t.Fatalf("edit = %d %+v", status, pending)
	}
	if pending.Approval.Args["old_text_preview"] == nil || pending.Approval.Args["new_text_preview"] == nil {
		t.Fatalf("the approval must show what is replaced and by what: %v", pending.Approval.Args)
	}
	if data, _ := f.fileOnDisk(t, "site/index.html"); data != original {
		t.Fatal("the file changed before the user approved")
	}

	approve := toolRequest(t, http.MethodPost, "/api/workspace-tools/approvals/"+pending.Approval.ID+"/approve", owner, nil)
	var done toolResponse
	_ = json.Unmarshal(approve.Body.Bytes(), &done)
	if approve.Code != http.StatusOK || done.Result["ok"] != true {
		t.Fatalf("approve = %d %s", approve.Code, approve.Body.String())
	}
	want := "<h1>Rina</h1>\r\n<p>Halo, saya Rina</p>\r\n<footer>2026</footer>\r\n"
	if data, _ := f.fileOnDisk(t, "site/index.html"); data != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
	rows := f.activities("workspace.edit_file")
	if len(rows) == 0 || strings.Contains(rows[len(rows)-1].ArgumentsJSON, "saya Rina") {
		t.Fatalf("the edited text must not be stored in Tool Activity: %+v", rows)
	}
}

package cloudworkspace

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newWorkspace(t *testing.T) (storage *LocalDevelopmentStorage, filesDir, metaDir string) {
	t.Helper()
	storage, err := NewLocalDevelopmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := storage.Create(7)
	if err != nil {
		t.Fatal(err)
	}
	filesDir, _ = storage.FilesDir(7, ref)
	metaDir, _ = storage.MetaDir(7, ref)
	return storage, filesDir, metaDir
}

func makeZip(t *testing.T, entries map[string]string) []byte {
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
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestCleanRelativeRefusesAnythingOutsideTheWorkspace(t *testing.T) {
	for _, bad := range []string{"/etc/passwd", `\windows\system32`, "C:/Windows", "c:\\x", "../secret", "a/../../b", "a/..", "..", "a\\..\\..\\b", "x\x00y"} {
		if _, err := CleanRelative(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for raw, want := range map[string]string{"": "", ".": "", "./a/b.txt": "a/b.txt", "a//b": "a/b", `src\main.go`: "src/main.go"} {
		if got, err := CleanRelative(raw); err != nil || got != want {
			t.Errorf("CleanRelative(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

func TestSecretFilesAreRecognised(t *testing.T) {
	for _, secret := range []string{".env", "app/.env", ".env.local", "config/prod.env", "id_rsa", "keys/server.pem", "tls.key", ".npmrc", "credentials.json", ".netrc"} {
		if !IsSecretPath(secret) {
			t.Errorf("%q should be treated as secret", secret)
		}
	}
	for _, fine := range []string{"main.go", "src/env.go", "README.md", "environment.ts", "keyboard.ts"} {
		if IsSecretPath(fine) {
			t.Errorf("%q should not be treated as secret", fine)
		}
	}
}

func TestWriteReadAndTree(t *testing.T) {
	_, files, _ := newWorkspace(t)
	limits := DefaultLimits()
	result, err := WriteFile(files, "src/app/main.go", "package main\n", limits, 1<<20)
	if err != nil || !result.Created || result.Size != 13 {
		t.Fatalf("write = %+v, %v", result, err)
	}
	if again, err := WriteFile(files, "src/app/main.go", "package main // v2\n", limits, 1<<20); err != nil || again.Created || again.Growth != 6 {
		t.Fatalf("overwrite = %+v, %v", again, err)
	}
	content, size, err := ReadFile(files, "src/app/main.go", limits)
	if err != nil || content != "package main // v2\n" || size != 19 {
		t.Fatalf("read = %q %d %v", content, size, err)
	}
	if _, err := WriteFile(files, "README.md", "# hi", limits, 1<<20); err != nil {
		t.Fatal(err)
	}
	tree, err := BuildTree(files, "", limits)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range tree.Entries {
		got = append(got, entry.Type+":"+entry.Path)
	}
	want := "dir:src|file:README.md|dir:src/app|file:src/app/main.go"
	if strings.Join(got, "|") != want {
		t.Fatalf("tree = %v", got)
	}
	sub, err := BuildTree(files, "src", limits)
	if err != nil || len(sub.Entries) != 2 {
		t.Fatalf("subtree = %+v, %v", sub, err)
	}
}

func TestFileLimitsAndRefusals(t *testing.T) {
	_, files, _ := newWorkspace(t)
	limits := DefaultLimits()
	limits.MaxFileWriteBytes = 10
	limits.MaxFileReadBytes = 5
	if _, err := WriteFile(files, "big.txt", strings.Repeat("a", 11), limits, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized write = %v", err)
	}
	if _, err := WriteFile(files, "ok.txt", "0123456789", limits, 5); !errors.Is(err, ErrQuota) {
		t.Errorf("write over the budget = %v", err)
	}
	if _, err := WriteFile(files, "bin.dat", "a\x00b", limits, 1<<20); !errors.Is(err, ErrBinary) {
		t.Errorf("binary write = %v", err)
	}
	for _, path := range []string{".env", "deploy/.env.production", "id_rsa", "server.pem"} {
		if _, err := WriteFile(files, path, "x", limits, 1<<20); !errors.Is(err, ErrSecretFile) {
			t.Errorf("write %q = %v", path, err)
		}
	}
	if _, err := WriteFile(files, "../outside.txt", "x", limits, 1<<20); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("traversal write = %v", err)
	}
	if _, err := WriteFile(files, "", "x", limits, 1<<20); !errors.Is(err, ErrIsDirectory) {
		t.Errorf("write to root = %v", err)
	}

	// A secret that already exists in the folder is still not readable.
	if err := os.WriteFile(filepath.Join(files, ".env"), []byte("TOKEN=abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(files, ".env", DefaultLimits()); !errors.Is(err, ErrSecretFile) {
		t.Errorf("read .env = %v", err)
	}
	_ = os.WriteFile(filepath.Join(files, "long.txt"), []byte("123456"), 0o640)
	if _, _, err := ReadFile(files, "long.txt", limits); !errors.Is(err, ErrTooLarge) {
		t.Errorf("read over the limit = %v", err)
	}
	_ = os.WriteFile(filepath.Join(files, "image.png"), []byte{0x89, 'P', 'N', 'G', 0, 1}, 0o640)
	if _, _, err := ReadFile(files, "image.png", DefaultLimits()); !errors.Is(err, ErrBinary) {
		t.Errorf("read binary = %v", err)
	}
	_ = os.WriteFile(filepath.Join(files, "latin1.txt"), []byte{0xff, 0xfe, 'a'}, 0o640)
	if _, _, err := ReadFile(files, "latin1.txt", DefaultLimits()); !errors.Is(err, ErrBinary) {
		t.Errorf("read invalid UTF-8 = %v", err)
	}
	if _, _, err := ReadFile(files, "nope.txt", DefaultLimits()); !errors.Is(err, ErrNotFound) {
		t.Errorf("read missing = %v", err)
	}
	_ = os.MkdirAll(filepath.Join(files, "dir"), 0o750)
	if _, _, err := ReadFile(files, "dir", DefaultLimits()); !errors.Is(err, ErrIsDirectory) {
		t.Errorf("read directory = %v", err)
	}
	if _, _, err := ReadFile(files, "/etc/passwd", DefaultLimits()); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("read absolute path = %v", err)
	}
}

func TestTreeIsBoundedAndHidesNothingButSymlinks(t *testing.T) {
	_, files, _ := newWorkspace(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		_ = os.WriteFile(filepath.Join(files, name), []byte("x"), 0o640)
	}
	_ = os.WriteFile(filepath.Join(files, ".env"), []byte("S=1"), 0o600)
	limits := DefaultLimits()
	limits.MaxTreeEntries = 3
	tree, err := BuildTree(files, "", limits)
	if err != nil || len(tree.Entries) != 3 || !tree.Truncated {
		t.Fatalf("tree = %+v, %v", tree, err)
	}
	full, _ := BuildTree(files, "", DefaultLimits())
	flagged := false
	for _, entry := range full.Entries {
		if entry.Path == ".env" {
			flagged = entry.Sensitive
		}
	}
	if !flagged {
		t.Error("the .env file should be listed and marked sensitive")
	}
}

func TestSymlinksCannotLeadOutOfTheWorkspace(t *testing.T) {
	_, files, _ := newWorkspace(t)
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("top secret"), 0o600)
	// Directory link: a symlink where allowed, otherwise a Windows junction.
	if err := os.Symlink(outside, filepath.Join(files, "link")); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("cannot create symlinks here: %v", err)
		}
		if out, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(files, "link"), outside).CombinedOutput(); junctionErr != nil {
			t.Skipf("cannot create a symlink or junction here: %v %s", junctionErr, out)
		}
	}
	fileLink := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(files, "file-link")) == nil
	limits := DefaultLimits()
	if _, _, err := ReadFile(files, "link/secret.txt", limits); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("read through a directory symlink = %v", err)
	}
	if fileLink {
		if _, _, err := ReadFile(files, "file-link", limits); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("read a file symlink = %v", err)
		}
		if _, err := WriteFile(files, "file-link", "overwritten", limits, 1<<20); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("write over a symlink = %v", err)
		}
	}
	if _, err := WriteFile(files, "link/new.txt", "x", limits, 1<<20); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("write through a symlink = %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "secret.txt")); string(data) != "top secret" {
		t.Fatal("a file outside the workspace was changed")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); err == nil {
		t.Fatal("a file was created outside the workspace")
	}
	tree, _ := BuildTree(files, "", limits)
	for _, entry := range tree.Entries {
		if strings.Contains(entry.Path, "link") {
			t.Errorf("symlink %q is listed", entry.Path)
		}
	}
}

func TestExtractZip(t *testing.T) {
	_, files, _ := newWorkspace(t)
	data := makeZip(t, map[string]string{
		"project-main/main.go":           "package main",
		"project-main/src/util.go":       "package src",
		"project-main/.env":              "TOKEN=abc",
		"project-main/config/server.pem": "-----BEGIN-----",
	})
	result, err := ExtractZip(data, files, DefaultLimits(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 2 || result.Skipped != 2 {
		t.Fatalf("result = %+v", result)
	}
	if _, _, err := ReadFile(files, "main.go", DefaultLimits()); err != nil {
		t.Errorf("the single top folder was not removed: %v", err)
	}
	for _, secret := range []string{".env", "config/server.pem"} {
		if _, err := os.Stat(filepath.Join(files, filepath.FromSlash(secret))); err == nil {
			t.Errorf("secret file %q was extracted", secret)
		}
	}
}

func TestExtractZipRefusesDangerousArchives(t *testing.T) {
	limits := DefaultLimits()
	for name, entries := range map[string]map[string]string{
		"parent traversal":    {"../evil.txt": "x"},
		"nested traversal":    {"a/../../evil.txt": "x"},
		"absolute":            {"/etc/evil.txt": "x"},
		"windows traversal":   {`..\evil.txt`: "x"},
		"windows drive":       {`C:\evil.txt`: "x"},
		"traversal after top": {"proj/../../evil.txt": "x", "proj/ok.txt": "y"},
	} {
		_, files, _ := newWorkspace(t)
		outside := filepath.Join(filepath.Dir(files), "evil.txt")
		if _, err := ExtractZip(makeZip(t, entries), files, limits, 1<<30); err == nil {
			t.Errorf("%s: accepted", name)
		}
		for _, escaped := range []string{outside, filepath.Join(filepath.Dir(filepath.Dir(files)), "evil.txt"), filepath.Join(files, "evil.txt")} {
			if _, err := os.Stat(escaped); err == nil {
				t.Errorf("%s: wrote %s", name, escaped)
			}
		}
	}

	_, files, _ := newWorkspace(t)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: "link", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)
	part, _ := writer.CreateHeader(header)
	_, _ = part.Write([]byte("/etc/passwd"))
	_ = writer.Close()
	if _, err := ExtractZip(buffer.Bytes(), files, limits, 1<<30); err == nil {
		t.Error("a symlink entry was accepted")
	}
	if _, err := ExtractZip([]byte("not a zip"), files, limits, 1<<30); err == nil {
		t.Error("a non-zip was accepted")
	}
	if _, err := ExtractZip(nil, files, limits, 1<<30); err == nil {
		t.Error("an empty upload was accepted")
	}
}

func TestExtractZipEnforcesSizeLimitsOnRealContent(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxExpandedBytes = 1000
	_, files, _ := newWorkspace(t)
	// 5000 bytes of zeros compress to almost nothing: a small archive that expands a lot.
	bomb := makeZip(t, map[string]string{"zeros.txt": strings.Repeat("0", 5000)})
	if int64(len(bomb)) > 200 {
		t.Fatalf("test archive should be tiny, got %d bytes", len(bomb))
	}
	if _, err := ExtractZip(bomb, files, limits, 1<<30); err == nil {
		t.Error("an archive that expands past the limit was accepted")
	}
	if _, err := ExtractZip(makeZip(t, map[string]string{"a.txt": strings.Repeat("a", 600)}), files, DefaultLimits(), 500); !errors.Is(err, ErrQuota) {
		t.Errorf("an archive over the user's budget = %v", err)
	}
	small := DefaultLimits()
	small.MaxZipFiles = 2
	many := makeZip(t, map[string]string{"1.txt": "a", "2.txt": "b", "3.txt": "c"})
	if _, err := ExtractZip(many, files, small, 1<<30); err == nil {
		t.Error("an archive with too many files was accepted")
	}
	small = DefaultLimits()
	small.MaxZipBytes = 10
	if _, err := ExtractZip(many, files, small, 1<<30); err == nil {
		t.Error("an oversized archive was accepted")
	}
}

func TestExportZipRoundTripsWithoutSecrets(t *testing.T) {
	_, files, _ := newWorkspace(t)
	limits := DefaultLimits()
	_, _ = WriteFile(files, "main.go", "package main", limits, 1<<20)
	_, _ = WriteFile(files, "pkg/a.go", "package pkg", limits, 1<<20)
	_ = os.WriteFile(filepath.Join(files, ".env"), []byte("TOKEN=abc"), 0o600)

	var out bytes.Buffer
	if err := ExportZip(files, &out, limits); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "pkg/a.go") || strings.Contains(joined, ".env") {
		t.Fatalf("export contains %v", names)
	}
	small := DefaultLimits()
	small.MaxExpandedBytes = 5
	if err := ExportZip(files, &bytes.Buffer{}, small); err == nil {
		t.Error("an export over the limit was produced")
	}
}

func TestChangesAgainstTheBaseline(t *testing.T) {
	_, files, meta := newWorkspace(t)
	limits := DefaultLimits()
	_, _ = WriteFile(files, "keep.txt", "same", limits, 1<<20)
	_, _ = WriteFile(files, "edit.txt", "before", limits, 1<<20)
	_, _ = WriteFile(files, "remove.txt", "bye", limits, 1<<20)
	if err := SaveBaseline(files, meta, limits); err != nil {
		t.Fatal(err)
	}
	if changes, err := Changes(files, meta, limits); err != nil || len(changes) != 0 {
		t.Fatalf("a fresh baseline has changes: %v %v", changes, err)
	}
	_, _ = WriteFile(files, "edit.txt", "after", limits, 1<<20)
	_, _ = WriteFile(files, "new/added.txt", "hello", limits, 1<<20)
	_ = os.Remove(filepath.Join(files, "remove.txt"))
	_ = os.WriteFile(filepath.Join(files, ".env"), []byte("S=1"), 0o600)

	changes, err := Changes(files, meta, limits)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, change := range changes {
		got[change.Path] = change.Status
	}
	want := map[string]string{"edit.txt": "modified", "new/added.txt": "added", "remove.txt": "deleted"}
	if len(got) != len(want) {
		t.Fatalf("changes = %v", got)
	}
	for path, status := range want {
		if got[path] != status {
			t.Errorf("%s = %q, want %q", path, got[path], status)
		}
	}
	// With no baseline at all everything counts as added.
	_, fresh, freshMeta := newWorkspace(t)
	_, _ = WriteFile(fresh, "a.txt", "a", limits, 1<<20)
	if changes, _ := Changes(fresh, freshMeta, limits); len(changes) != 1 || changes[0].Status != "added" {
		t.Errorf("no baseline: %v", changes)
	}
}

func TestStorageKeepsUsersApart(t *testing.T) {
	storage, err := NewLocalDevelopmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := storage.Create(1)
	mine, _ := storage.FilesDir(1, ref)
	theirs, _ := storage.FilesDir(2, ref) // the same reference under another user
	if mine == theirs {
		t.Fatal("two users share a directory")
	}
	if _, err := os.Stat(theirs); err == nil {
		t.Fatal("another user's path exists for a workspace they do not own")
	}
	for _, bad := range []string{"", "../x", "abc", strings.Repeat("g", 32), ref + "/../.."} {
		if _, err := storage.FilesDir(1, bad); err == nil {
			t.Errorf("reference %q was accepted", bad)
		}
	}
	if _, err := storage.FilesDir(0, ref); err == nil {
		t.Error("user 0 was accepted")
	}
	_, _ = WriteFile(mine, "a.txt", "12345", DefaultLimits(), 1<<20)
	if bytes, files, err := storage.Usage(1, ref); err != nil || bytes != 5 || files != 1 {
		t.Errorf("usage = %d %d %v", bytes, files, err)
	}
	if err := storage.Delete(1, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mine); err == nil {
		t.Error("the workspace still exists after Delete")
	}
}

func TestParseGitHubURL(t *testing.T) {
	good := map[string][2]string{
		"https://github.com/golang/go":           {"golang", "go"},
		"https://github.com/golang/go.git":       {"golang", "go"},
		"https://github.com/golang/go/":          {"golang", "go"},
		" https://GitHub.com/owner/my-repo_1.x ": {"owner", "my-repo_1.x"},
	}
	for raw, want := range good {
		owner, repo, err := ParseGitHubURL(raw)
		if err != nil || owner != want[0] || repo != want[1] {
			t.Errorf("%q = %q/%q, %v", raw, owner, repo, err)
		}
	}
	for _, bad := range []string{
		"http://github.com/a/b",
		"https://user:pass@github.com/a/b",
		"https://token@github.com/a/b",
		"https://github.com:8443/a/b",
		"https://evil.com/a/b",
		"https://github.com.evil.com/a/b",
		"https://api.github.com/a/b",
		"https://127.0.0.1/a/b",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/a/b",
		"https://localhost/a/b",
		"https://github.com/a",
		"https://github.com/a/b/tree/main",
		"https://github.com/a/b?x=1",
		"https://github.com/a/b#frag",
		"https://github.com/../b",
		"https://github.com/a/..",
		"ssh://git@github.com/a/b",
		"git@github.com:a/b.git",
		"file:///etc/passwd",
		"",
	} {
		if _, _, err := ParseGitHubURL(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestBlockedIPCoversInternalAddresses(t *testing.T) {
	for _, internal := range []string{"127.0.0.1", "::1", "10.0.0.5", "172.16.3.4", "192.168.1.1", "169.254.169.254", "169.254.1.1", "0.0.0.0", "100.100.100.200", "fe80::1", "fc00::1", "224.0.0.1"} {
		if !BlockedIP(net.ParseIP(internal)) {
			t.Errorf("%s should be blocked", internal)
		}
	}
	for _, public := range []string{"140.82.112.4", "8.8.8.8", "2606:4700:4700::1111"} {
		if BlockedIP(net.ParseIP(public)) {
			t.Errorf("%s should be allowed", public)
		}
	}
	if !BlockedIP(nil) {
		t.Error("an unparsable address must be blocked")
	}
}

func TestDownloaderCannotReachInternalServers(t *testing.T) {
	var reached bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer internal.Close()
	downloader := NewGitHubDownloader() // the real client, with the connection guard
	downloader.BaseURL = internal.URL   // 127.0.0.1
	if _, err := downloader.Download(context.Background(), "a", "b", DefaultLimits()); err == nil {
		t.Fatal("the downloader connected to a loopback server")
	}
	if reached {
		t.Fatal("the internal server received a request")
	}
}

func TestDownloaderLimitsAndErrors(t *testing.T) {
	archive := makeZip(t, map[string]string{"repo-main/a.txt": "hello"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/missing/"):
			http.NotFound(w, r)
		case strings.Contains(r.URL.Path, "/huge/"):
			_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
		case strings.Contains(r.URL.Path, "/redirect/"):
			http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusFound)
		default:
			_, _ = w.Write(archive)
		}
	}))
	defer server.Close()
	downloader := &GitHubDownloader{BaseURL: server.URL, Client: &http.Client{CheckRedirect: NewGitHubDownloader().Client.CheckRedirect}}

	data, err := downloader.Download(context.Background(), "owner", "repo", DefaultLimits())
	if err != nil || !bytes.Equal(data, archive) {
		t.Fatalf("download = %d bytes, %v", len(data), err)
	}
	if _, err := downloader.Download(context.Background(), "missing", "repo", DefaultLimits()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("404 = %v", err)
	}
	small := DefaultLimits()
	small.MaxZipBytes = 1000
	if _, err := downloader.Download(context.Background(), "huge", "repo", small); err == nil {
		t.Error("an oversized download was accepted")
	}
	if _, err := downloader.Download(context.Background(), "redirect", "repo", DefaultLimits()); err == nil {
		t.Error("a redirect to another host was followed")
	}
}

func TestDownloaderExplainsAnEmptyRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/empty/commits":
			w.WriteHeader(http.StatusConflict) // GitHub: "Git Repository is empty."
		default:
			http.NotFound(w, r) // the archive of an empty repository, and of one that does not exist
		}
	}))
	defer server.Close()
	downloader := &GitHubDownloader{BaseURL: server.URL, APIURL: server.URL, Client: &http.Client{}}

	if _, err := downloader.Download(context.Background(), "owner", "empty", DefaultLimits()); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty repository should say so, got %v", err)
	}
	if _, err := downloader.Download(context.Background(), "owner", "gone", DefaultLimits()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a missing repository should say not found, got %v", err)
	}
	// If the lookup itself fails, the general message is still given.
	downloader.APIURL = "http://127.0.0.1:1"
	if _, err := downloader.Download(context.Background(), "owner", "empty", DefaultLimits()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a failed lookup should fall back to the general message, got %v", err)
	}
}

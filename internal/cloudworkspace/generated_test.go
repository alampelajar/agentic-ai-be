package cloudworkspace

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installed lays out what an install job leaves behind next to project files.
func installed(t *testing.T, files string) {
	t.Helper()
	for name, content := range map[string]string{
		"node_modules/left-pad/index.js":        "module.exports = 1",
		"node_modules/left-pad/package.json":    "{}",
		"web/node_modules/react/index.js":       "x",
		".pydeps/six.py":                        "x",
		"node_modules/.package-lock.json":       "{}",
		"src/node_modules_notes.txt":            "not a dependency folder",
		"src/pydeps.py":                         "not a dependency folder either",
		"vendor/github.com/google/uuid/uuid.go": "package uuid",
	} {
		path := filepath.Join(files, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGeneratedPathsAreRecognisedByNameNotBySubstring(t *testing.T) {
	for _, path := range []string{"node_modules", "node_modules/x/y.js", "web/node_modules/react", ".pydeps/six.py", "a/b/.pydeps"} {
		if !IsGeneratedPath(path) {
			t.Errorf("%q should be a generated path", path)
		}
	}
	for _, path := range []string{"", "src/main.go", "node_modules_notes.txt", "my_node_modules/x", "pydeps/x", ".pydeps2/x", "vendor/x", "src/node-modules/x"} {
		if IsGeneratedPath(path) {
			t.Errorf("%q must not be a generated path", path)
		}
	}
}

func TestDependencyFoldersAreListedButNeverExpanded(t *testing.T) {
	_, files, _ := newWorkspace(t)
	installed(t, files)
	tree, err := BuildTree(files, "", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]TreeEntry{}
	for _, entry := range tree.Entries {
		seen[entry.Path] = entry
	}
	for _, folder := range []string{"node_modules", ".pydeps", "web/node_modules"} {
		if entry, ok := seen[folder]; !ok || !entry.Generated || entry.Type != "dir" {
			t.Errorf("%s = %+v, want a generated dir", folder, entry)
		}
	}
	for path := range seen {
		if strings.HasPrefix(path, "node_modules/") || strings.HasPrefix(path, ".pydeps/") || strings.HasPrefix(path, "web/node_modules/") {
			t.Errorf("%s is listed inside a dependency folder", path)
		}
	}
	// Ordinary look-alikes and vendor/ stay visible.
	for _, path := range []string{"src/node_modules_notes.txt", "src/pydeps.py", "vendor/github.com/google/uuid/uuid.go"} {
		if entry, ok := seen[path]; !ok || entry.Generated {
			t.Errorf("%s = %+v, want an ordinary entry", path, entry)
		}
	}
	// Opening one of those folders directly lists nothing.
	inside, err := BuildTree(files, "node_modules/left-pad", DefaultLimits())
	if err != nil || len(inside.Entries) != 0 {
		t.Errorf("inside = %+v, %v", inside, err)
	}
}

func TestDependencyFoldersCannotBeReadOrWrittenThroughTheAPI(t *testing.T) {
	_, files, _ := newWorkspace(t)
	installed(t, files)
	limits := DefaultLimits()
	for _, path := range []string{"node_modules/left-pad/index.js", "web/node_modules/react/index.js", ".pydeps/six.py"} {
		if _, _, err := ReadFile(files, path, limits); !errors.Is(err, ErrGeneratedPath) {
			t.Errorf("read %s = %v", path, err)
		}
		if _, err := WriteFile(files, path, "changed", limits, 1<<20); !errors.Is(err, ErrGeneratedPath) {
			t.Errorf("write %s = %v", path, err)
		}
	}
	// A new file in a dependency folder is refused too, so nothing can be planted.
	if _, err := WriteFile(files, "node_modules/evil/index.js", "x", limits, 1<<20); !errors.Is(err, ErrGeneratedPath) {
		t.Errorf("planting a package = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(files, "node_modules/left-pad/index.js")); string(got) != "module.exports = 1" {
		t.Errorf("a dependency file changed: %q", got)
	}
	if _, _, err := ReadFile(files, "src/pydeps.py", limits); err != nil {
		t.Errorf("an ordinary file with a similar name is unreadable: %v", err)
	}
}

func TestDependenciesAreNotChangesNorExportedNorCountedAsProjectSize(t *testing.T) {
	_, files, meta := newWorkspace(t)
	limits := DefaultLimits()
	_, _ = WriteFile(files, "main.go", "package main", limits, 1<<20)
	if err := SaveBaseline(files, meta, limits); err != nil {
		t.Fatal(err)
	}
	installed(t, files)

	changes, err := Changes(files, meta, limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		if IsGeneratedPath(change.Path) {
			t.Errorf("%s is reported as a change", change.Path)
		}
	}
	if len(changes) != 3 { // the three ordinary files that installed() adds
		t.Errorf("changes = %v", changes)
	}

	var archive bytes.Buffer
	if err := ExportZip(files, &archive, limits); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if IsGeneratedPath(file.Name) {
			t.Errorf("%s was exported", file.Name)
		}
	}

	bytesUsed, count, err := DirUsage(files)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := int64(len("package main") + len("not a dependency folder") + len("not a dependency folder either") + len("package uuid"))
	if bytesUsed != wantBytes || count != 4 {
		t.Errorf("project usage = %d bytes in %d files, want %d in 4", bytesUsed, count, wantBytes)
	}
	generated, err := GeneratedUsage(files)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(len("module.exports = 1") + 2 + len("x") + len("x") + 2); generated != want {
		t.Errorf("dependency usage = %d, want %d", generated, want)
	}
}

func TestUploadedArchivesLeaveDependencyFoldersOut(t *testing.T) {
	_, files, _ := newWorkspace(t)
	data := makeZip(t, map[string]string{
		"app/main.go":                     "package main",
		"app/node_modules/x/index.js":     "x",
		"app/node_modules/.bin/tool":      "#!/bin/sh",
		"app/web/node_modules/y/index.js": "y",
		"app/.pydeps/z.py":                "z",
	})
	result, err := ExtractZip(data, files, DefaultLimits(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Skipped != 4 {
		t.Fatalf("result = %+v", result)
	}
	for _, folder := range []string{"node_modules", "web/node_modules", ".pydeps"} {
		if _, err := os.Stat(filepath.Join(files, filepath.FromSlash(folder))); err == nil {
			t.Errorf("%s was created from the archive", folder)
		}
	}
}

func TestRemoveGeneratedDeletesOnlyDependencyFolders(t *testing.T) {
	_, files, _ := newWorkspace(t)
	installed(t, files)
	// A symlink out of a dependency folder must be removed, never followed.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "precious.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(files, "node_modules", "escape")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlinks are not available here: %v", err)
		}
		// Windows without the symlink privilege: a junction is the same escape route.
		if out, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); junctionErr != nil {
			t.Skipf("cannot create a symlink or junction here: %v %s", junctionErr, out)
		}
	}
	if err := RemoveGenerated(files); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"node_modules", "web/node_modules", ".pydeps"} {
		if _, err := os.Stat(filepath.Join(files, filepath.FromSlash(folder))); err == nil {
			t.Errorf("%s survived", folder)
		}
	}
	for _, kept := range []string{"src/node_modules_notes.txt", "src/pydeps.py", "vendor/github.com/google/uuid/uuid.go", "web"} {
		if _, err := os.Stat(filepath.Join(files, filepath.FromSlash(kept))); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(outside, "precious.txt")); err != nil || string(data) != "keep me" {
		t.Fatalf("the symlink target was touched: %q, %v", data, err)
	}
}

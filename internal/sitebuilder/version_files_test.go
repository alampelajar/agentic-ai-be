package sitebuilder

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSource(t *testing.T, versionDir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		target := filepath.Join(versionDir, "source", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

// The owner reads the code of a version and sees what a change did to it.
func TestCompareVersionsAndReadFiles(t *testing.T) {
	root := t.TempDir()
	one, two := filepath.Join(root, "1"), filepath.Join(root, "2")
	writeSource(t, one, map[string]string{
		"web/src/pages/Home.tsx":   "home",
		"web/src/pages/About.tsx":  "about",
		"site.json":                "{}",
		"web/public/uploads/a.png": "png",
	})
	writeSource(t, two, map[string]string{
		"web/src/pages/Home.tsx":    "home, changed",
		"web/src/pages/Contact.tsx": "contact",
		"site.json":                 "{}",
		"web/public/uploads/a.png":  "png",
	})

	files, err := CompareVersions(two, one)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, file := range files {
		got[file.Path] = file.Change
	}
	want := map[string]string{
		"site.json":                 "",
		"web/src/pages/About.tsx":   "removed",
		"web/src/pages/Contact.tsx": "added",
		"web/src/pages/Home.tsx":    "changed",
	}
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v (pictures are not listed)", got, want)
	}
	for rel, change := range want {
		if got[rel] != change {
			t.Errorf("%s = %q, want %q", rel, got[rel], change)
		}
	}

	// The first version has nothing to compare with.
	first, err := CompareVersions(one, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range first {
		if file.Change != "" {
			t.Errorf("first version marks %s as %q", file.Path, file.Change)
		}
	}

	if content, found, err := ReadVersionFile(two, "web/src/pages/Home.tsx"); err != nil || !found || content != "home, changed" {
		t.Errorf("read Home = %q %v %v", content, found, err)
	}
	if _, found, err := ReadVersionFile(two, "web/src/pages/About.tsx"); err != nil || found {
		t.Errorf("a removed file = %v %v, want not found", found, err)
	}
	for _, refused := range []string{"../1/source/site.json", "/etc/passwd", "web/public/uploads/a.png", "", `C:\x.tsx`} {
		if _, _, err := ReadVersionFile(two, refused); err != ErrNotReadable {
			t.Errorf("%q = %v, want ErrNotReadable", refused, err)
		}
	}
}

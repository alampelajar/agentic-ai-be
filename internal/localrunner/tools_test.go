package localrunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRejectsWorkspaceTraversal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	w := Workspace{ID: "test", Name: "test", Path: root}
	s := &Server{}
	if _, err := s.resolve(w, filepath.Join("..", filepath.Base(outside), "secret.txt"), true); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}
	s := &Server{}
	if _, err := s.resolve(Workspace{Path: root}, filepath.Join("escape", "secret.txt"), false); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestActionCommandRejectsUnknownAction(t *testing.T) {
	if _, err := actionCommand(t.TempDir(), "powershell -Command whoami"); err == nil {
		t.Fatal("unknown commands must not be executable")
	}
}

func TestActionCommandDetectsGoProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args, err := actionCommand(root, "go-test")
	if err != nil { t.Fatal(err) }
	if len(args) != 3 || args[0] != "go" || args[1] != "test" || args[2] != "./..." {
		t.Fatalf("unexpected command: %#v", args)
	}
}

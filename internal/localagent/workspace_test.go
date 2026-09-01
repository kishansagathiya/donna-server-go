package localagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspacePathRejectsEscape(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "src")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveWorkspacePath(root, "src")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(inside)
	if err != nil {
		want = inside
	}
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}

	if _, err := ResolveWorkspacePath(root, "../etc/passwd"); err == nil {
		t.Fatal("expected traversal reject")
	}
	if _, err := ResolveWorkspacePath(root, "/etc/passwd"); err == nil {
		t.Fatal("expected absolute reject")
	}

	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveWorkspacePath(root, "escape"); err == nil {
		t.Fatal("expected symlink escape reject")
	}
}

func TestResolveWorkspacePathRequiresRoot(t *testing.T) {
	if _, err := ResolveWorkspacePath("", "src"); err == nil {
		t.Fatal("expected workspace_required")
	}
}

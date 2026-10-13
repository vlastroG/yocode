// Package testutil holds helpers shared by tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Git runs a git command in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// GitRepo creates a temporary git repository on branch main with one commit ("initial")
// containing README.md = "hello\n".
func GitRepo(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-b", "main")
	Git(t, dir, "config", "user.name", "test")
	Git(t, dir, "config", "user.email", "test@example.com")
	Git(t, dir, "config", "core.autocrlf", "false")
	Git(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "add", ".")
	Git(t, dir, "commit", "-m", "initial")
	return dir
}

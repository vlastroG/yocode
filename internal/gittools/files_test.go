package gittools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/testutil"
)

func readDisk(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriteEditDelete(t *testing.T) {
	dir := testutil.GitRepo(t)
	r := Repo{Dir: dir}

	out, err := r.WriteFile("docs/new/a.md", "x\n")
	if err != nil || !strings.Contains(out, "docs/new/a.md") {
		t.Fatalf("WriteFile = %q, %v", out, err)
	}
	if got := readDisk(t, dir, "docs/new/a.md"); got != "x\n" {
		t.Errorf("content = %q", got)
	}

	if _, err := r.EditFile("README.md", "hello", "bye"); err != nil {
		t.Fatal(err)
	}
	if got := readDisk(t, dir, "README.md"); got != "bye\n" {
		t.Errorf("README = %q", got)
	}
	if _, err := r.EditFile("README.md", "absent", "y"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing old_text error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "dup.txt"), []byte("a a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EditFile("dup.txt", "a", "b"); err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Errorf("duplicate error = %v", err)
	}
	if got := readDisk(t, dir, "dup.txt"); got != "a a" {
		t.Errorf("dup.txt changed: %q", got)
	}

	if _, err := r.DeleteFile("docs/new/a.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs/new/a.md")); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}
	if _, err := r.DeleteFile("docs"); err == nil {
		t.Error("deleting a directory should fail")
	}
}

func TestEditFilePreservesCRLF(t *testing.T) {
	dir := testutil.GitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "w.txt"), []byte("one\r\ntwo\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Repo{Dir: dir}).EditFile("w.txt", "one\ntwo", "uno\ndos"); err != nil {
		t.Fatal(err)
	}
	if got := readDisk(t, dir, "w.txt"); got != "uno\r\ndos\r\n" {
		t.Errorf("content = %q", got)
	}
}

func TestWritesRejectUnsafePaths(t *testing.T) {
	dir := testutil.GitRepo(t)
	r := Repo{Dir: dir}
	for _, p := range []string{".git/config", ".GIT/x", "../x", "/etc/x"} {
		if _, err := r.WriteFile(p, "x"); err == nil {
			t.Errorf("WriteFile(%q) should fail", p)
		}
	}
	if _, err := r.DeleteFile(".git/HEAD"); err == nil {
		t.Error("DeleteFile(.git/HEAD) should fail")
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Log("symlinks unavailable, skipping:", err)
		return
	}
	if _, err := r.WriteFile("out/x.txt", "x"); err == nil {
		t.Error("write through symlink should fail")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("outside dir was written to: %v", entries)
	}
}

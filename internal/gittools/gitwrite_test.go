package gittools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/testutil"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCreateBranchAndCommit(t *testing.T) {
	dir := testutil.GitRepo(t)
	r := Repo{Dir: dir}
	ctx := context.Background()

	if _, err := r.CreateBranch(ctx, "feature/x", ""); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, dir, "branch", "--show-current"); got != "feature/x" {
		t.Errorf("branch = %q", got)
	}
	for _, bad := range []string{"-bad", "a b"} {
		if _, err := r.CreateBranch(ctx, bad, ""); err == nil {
			t.Errorf("CreateBranch(%q) should fail", bad)
		}
	}
	if _, err := r.CreateBranch(ctx, "from-main", "main"); err != nil {
		t.Errorf("CreateBranch from main: %v", err)
	}

	for _, f := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := r.Commit(ctx, "add a", []string{"a.txt"})
	if err != nil || !strings.Contains(out, "add a") {
		t.Fatalf("Commit = %q, %v", out, err)
	}
	if got := gitOut(t, dir, "status", "--short"); got != "?? b.txt" {
		t.Errorf("status = %q", got)
	}
	if _, err := r.Commit(ctx, "add b", nil); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, dir, "status", "--short"); got != "" {
		t.Errorf("status = %q", got)
	}
	if _, err := r.Commit(ctx, "again", nil); err == nil || !strings.Contains(err.Error(), "nothing to commit") {
		t.Errorf("err = %v", err)
	}
	if _, err := r.Commit(ctx, "", nil); err == nil {
		t.Error("empty message should fail")
	}
	if _, err := r.Commit(ctx, "x", []string{"../y"}); err == nil {
		t.Error("escaping path should fail")
	}
}

func TestCommitPathsLeavesStagedWork(t *testing.T) {
	dir := testutil.GitRepo(t)
	ctx := context.Background()
	for _, f := range []string{"user.txt", "task.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, dir, "add", "user.txt")
	// a hook must not run inside yocode's commits
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "config", "commit.gpgsign", "true")
	if _, err := (Repo{Dir: dir}).Commit(ctx, "task", []string{"task.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD"); got != "task.txt" {
		t.Errorf("committed = %q", got)
	}
	if got := gitOut(t, dir, "status", "--short"); got != "A  user.txt" {
		t.Errorf("status = %q", got)
	}
	if _, err := (Repo{Dir: dir}).Commit(ctx, "again", []string{"task.txt"}); err == nil || !strings.Contains(err.Error(), "nothing to commit") {
		t.Errorf("err = %v", err)
	}
}

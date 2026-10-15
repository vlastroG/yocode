package gittools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/testutil"
)

func TestReadOnlyCommands(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Repo{Dir: dir}

	cases := []struct {
		name string
		run  func() (string, error)
		want string
	}{
		{"status", func() (string, error) { return r.Status(ctx) }, "README.md"},
		{"log", func() (string, error) { return r.Log(ctx, 5, "", "") }, "initial"},
		{"diff", func() (string, error) { return r.Diff(ctx, "", false, "") }, "+changed"},
		{"branches", func() (string, error) { return r.Branches(ctx) }, "main"},
		{"ls-files", func() (string, error) { return r.ListFiles(ctx, "") }, "README.md"},
		{"read", func() (string, error) { return r.ReadFile("README.md") }, "changed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := c.run()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("output %q does not contain %q", out, c.want)
			}
		})
	}
}

func TestRejectsUnsafeInput(t *testing.T) {
	ctx := context.Background()
	r := Repo{Dir: testutil.GitRepo(t)}

	if _, err := r.ReadFile("../outside.txt"); err == nil {
		t.Error("ReadFile escaped the project")
	}
	if _, err := r.Diff(ctx, "--output=/tmp/x", false, ""); err == nil {
		t.Error("Diff accepted an option as ref")
	}
	if _, err := r.Log(ctx, 5, "", "../.."); err == nil {
		t.Error("Log accepted a path outside the project")
	}
}

func TestReadFileRejectsBinary(t *testing.T) {
	dir := testutil.GitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Repo{Dir: dir}).ReadFile("bin.dat"); err == nil {
		t.Error("expected error for binary file")
	}
}

func TestBranchComparison(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)
	testutil.Git(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", ".")
	testutil.Git(t, dir, "commit", "-q", "-m", "feature work")
	r := Repo{Dir: dir}

	changed, err := r.ChangedFiles(ctx, "main...feature")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(changed, "A\tnew.go") || !strings.Contains(changed, "M\tREADME.md") {
		t.Errorf("changed = %q", changed)
	}

	log, err := r.Log(ctx, 10, "main..feature", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "feature work") || strings.Contains(log, "initial") {
		t.Errorf("log = %q", log)
	}

	if _, err := r.ChangedFiles(ctx, "--output=x"); err == nil {
		t.Error("ChangedFiles accepted an option as ref")
	}
	if _, err := r.ChangedFiles(ctx, ""); err == nil {
		t.Error("ChangedFiles accepted an empty ref")
	}
	if _, err := r.Log(ctx, 5, "--all", ""); err == nil {
		t.Error("Log accepted an option as ref")
	}
}

func TestGrep(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package x\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Repo{Dir: dir}

	out, err := r.Grep(ctx, "hello", "", true, false)
	if err != nil || !strings.Contains(out, "README.md:1:") || !strings.Contains(out, "new.go:") {
		t.Errorf("ignore-case grep = %q, %v", out, err)
	}
	out, err = r.Grep(ctx, "nomatch-xyz", "", false, false)
	if err != nil || out != "(no matches)" {
		t.Errorf("no-match grep = %q, %v", out, err)
	}
	out, err = r.Grep(ctx, "H.llo", "", false, true)
	if err != nil || !strings.Contains(out, "new.go:") {
		t.Errorf("regex grep = %q, %v", out, err)
	}
	if _, err := r.Grep(ctx, "", "", false, false); err == nil {
		t.Error("Grep accepted an empty pattern")
	}
	if _, err := r.Grep(ctx, "x", "../..", false, false); err == nil {
		t.Error("Grep accepted a path outside the project")
	}
}

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
		{"log", func() (string, error) { return r.Log(ctx, 5, "") }, "initial"},
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
	if _, err := r.Log(ctx, 5, "../.."); err == nil {
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

package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/gittools"
	"github.com/vlastroG/yocode/internal/mcpclient"
	"github.com/vlastroG/yocode/internal/mcpserver"
	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/testutil"
)

func TestDiffIncludesUntrackedFiles(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(gittools.Repo{Dir: dir}).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	c, err := mcpclient.Connect(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tools := c.Only(mcpserver.ReadTools...)

	if got, err := WorkingDiff(ctx, tools, ""); err != nil || got != "No changes." {
		t.Fatalf("clean repo = %q, %v", got, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "new.md"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "bin.dat"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := WorkingDiff(ctx, tools, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+changed", "diff --git a/docs/new.md b/docs/new.md", "new file", "@@ -0,0 +1,2 @@", "+a", "+b", "Binary file docs/bin.dat added"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	got, err = WorkingDiff(ctx, tools, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "README.md") || !strings.Contains(got, "docs/new.md") {
		t.Errorf("path filter failed:\n%s", got)
	}
}

func TestDiffCheck(t *testing.T) {
	c, _ := New(Deps{}).Lookup("diff")
	if c.Check([]string{"a", "b"}) == nil || c.Check(nil) != nil || c.Check([]string{"docs"}) != nil {
		t.Error("diff accepts zero or one argument")
	}
}

func TestDiffNonASCIIUntracked(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)
	testutil.Git(t, dir, "config", "core.quotePath", "true")
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(gittools.Repo{Dir: dir}).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	c, err := mcpclient.Connect(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "обзор.md"), []byte("привет\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := WorkingDiff(ctx, c.Only(mcpserver.ReadTools...), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "+++ b/docs/обзор.md") || !strings.Contains(got, "+привет") {
		t.Errorf("diff:\n%s", got)
	}
}

// failingRead lists one untracked file whose read fails.
type failingRead struct{}

func (failingRead) Tools(context.Context) ([]ollama.Tool, error) { return nil, nil }
func (failingRead) Call(_ context.Context, name string, _ map[string]any) (string, error) {
	switch name {
	case "git_status":
		return "## main\n?? gone.txt\n?? ok.txt\n", nil
	case "read_file":
		return "", errors.New("no such file")
	}
	return "(no output)", nil
}

func TestDiffReadFailureIsANote(t *testing.T) {
	got, err := WorkingDiff(context.Background(), failingRead{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "gone.txt") || !strings.Contains(got, "no such file") || !strings.Contains(got, "ok.txt") {
		t.Errorf("diff:\n%s", got)
	}
}

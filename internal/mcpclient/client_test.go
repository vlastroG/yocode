package mcpclient

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/gittools"
	"github.com/vlastroG/yocode/internal/mcpserver"
	"github.com/vlastroG/yocode/internal/testutil"
)

func TestClientListsAndCallsTools(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(gittools.Repo{Dir: dir}).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	c, err := Connect(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	tools, err := c.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Function.Name] = true
		if tool.Type != "function" {
			t.Errorf("tool %s type = %q", tool.Function.Name, tool.Type)
		}
		if tool.Function.Name == "read_file" && !strings.Contains(string(tool.Function.Parameters), `"path"`) {
			t.Errorf("read_file schema = %s", tool.Function.Parameters)
		}
	}
	for _, want := range []string{"git_status", "git_log", "git_diff", "git_changed_files", "git_branch", "list_files", "read_file", "grep", "write_file", "edit_file", "delete_file", "git_create_branch", "git_commit"} {
		if !names[want] {
			t.Errorf("missing tool %s (got %v)", want, names)
		}
	}

	out, err := c.Call(ctx, "read_file", map[string]any{"path": "README.md"})
	if err != nil || !strings.Contains(out, "hello") {
		t.Errorf("read_file = %q, %v", out, err)
	}
	if _, err := c.Call(ctx, "read_file", map[string]any{"path": "../x"}); err == nil {
		t.Error("expected error for path outside project")
	}
	out, err = c.Call(ctx, "git_status", nil)
	if err != nil || !strings.Contains(out, "main") {
		t.Errorf("git_status = %q, %v", out, err)
	}
	out, err = c.Call(ctx, "git_changed_files", map[string]any{"ref": "main...main"})
	if err != nil || out != "(no output)" {
		t.Errorf("git_changed_files = %q, %v", out, err)
	}
}

func TestOnlyFiltersToolsAndCalls(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(gittools.Repo{Dir: dir}).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	c, err := Connect(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	v := c.Only("read_file")
	tools, err := v.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Function.Name != "read_file" {
		t.Errorf("tools = %v", tools)
	}
	if _, err := v.Call(ctx, "git_status", nil); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("git_status err = %v", err)
	}
	if out, err := v.Call(ctx, "read_file", map[string]any{"path": "README.md"}); err != nil || !strings.Contains(out, "hello") {
		t.Errorf("read_file = %q, %v", out, err)
	}
}

// Package mcpserver exposes read-only project tools over MCP.
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/gittools"
)

type noArgs struct{}

type logArgs struct {
	N    int    `json:"n,omitempty" jsonschema:"number of commits to show (default 20)"`
	Ref  string `json:"ref,omitempty" jsonschema:"branch, commit or range such as main..feature; empty means the current branch"`
	Path string `json:"path,omitempty" jsonschema:"only commits touching this file or directory"`
}

type changesArgs struct {
	Ref string `json:"ref" jsonschema:"range to compare, e.g. main...feature (three dots: changes on feature since it branched off main)"`
}

type diffArgs struct {
	Ref    string `json:"ref,omitempty" jsonschema:"commit, branch or range such as main..feature; empty means the working tree"`
	Staged bool   `json:"staged,omitempty" jsonschema:"show staged changes instead of unstaged ones"`
	Path   string `json:"path,omitempty" jsonschema:"limit the diff to this file or directory"`
}

type dirArgs struct {
	Path string `json:"path,omitempty" jsonschema:"directory relative to the project root; empty means the whole project"`
}

type fileArgs struct {
	Path string `json:"path" jsonschema:"file path relative to the project root"`
}

func New(repo gittools.Repo) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "yocode-git", Version: "0.1.0"}, nil)
	add(s, "git_status", "Show the working tree status (short format) and the current branch.",
		func(ctx context.Context, _ noArgs) (string, error) { return repo.Status(ctx) })
	add(s, "git_log", "Show commits (hash, date, author, subject) of the current branch, a branch, or a range.",
		func(ctx context.Context, a logArgs) (string, error) { return repo.Log(ctx, a.N, a.Ref, a.Path) })
	add(s, "git_diff", "Show a diff of the working tree, the staged changes, or against a ref.",
		func(ctx context.Context, a diffArgs) (string, error) { return repo.Diff(ctx, a.Ref, a.Staged, a.Path) })
	add(s, "git_changed_files", "List files changed in a range (status letter and path), e.g. the files of a pull request.",
		func(ctx context.Context, a changesArgs) (string, error) { return repo.ChangedFiles(ctx, a.Ref) })
	add(s, "git_branch", "List local and remote branches.",
		func(ctx context.Context, _ noArgs) (string, error) { return repo.Branches(ctx) })
	add(s, "list_files", "List files tracked by git, optionally under a directory.",
		func(ctx context.Context, a dirArgs) (string, error) { return repo.ListFiles(ctx, a.Path) })
	add(s, "read_file", "Read a text file from the project.",
		func(_ context.Context, a fileArgs) (string, error) { return repo.ReadFile(a.Path) })
	return s
}

// add registers a tool whose result is plain text; errors are reported to the model as tool errors.
func add[In any](s *mcp.Server, name, desc string, fn func(context.Context, In) (string, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			out, err := fn(ctx, in)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})
}

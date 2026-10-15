// Package mcpserver exposes project tools (reads, file edits) over MCP.
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

type writeArgs struct {
	Path    string `json:"path" jsonschema:"file path relative to the project root"`
	Content string `json:"content" jsonschema:"the complete new content of the file"`
}

type editArgs struct {
	Path    string `json:"path" jsonschema:"file path relative to the project root"`
	OldText string `json:"old_text" jsonschema:"exact text to replace; must occur exactly once in the file"`
	NewText string `json:"new_text" jsonschema:"replacement text"`
}

type branchArgs struct {
	Name string `json:"name" jsonschema:"new branch name, e.g. feature/docs-update"`
	From string `json:"from,omitempty" jsonschema:"branch or commit to start from; empty means the current HEAD"`
}

type commitArgs struct {
	Message string   `json:"message" jsonschema:"commit message"`
	Paths   []string `json:"paths,omitempty" jsonschema:"files the task changed, relative to the project root; empty commits all changes"`
}

type grepArgs struct {
	Pattern    string `json:"pattern" jsonschema:"text to search for (a regular expression when regex is true)"`
	Path       string `json:"path,omitempty" jsonschema:"limit the search to this file or directory"`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"match case-insensitively"`
	Regex      bool   `json:"regex,omitempty" jsonschema:"treat pattern as an extended regular expression instead of plain text"`
}

// Tool subsets for consumers that get only part of the toolbox.
var (
	ReadTools     = []string{"git_status", "git_log", "git_diff", "git_changed_files", "git_branch", "list_files", "read_file", "grep"}
	EditTools     = []string{"write_file", "edit_file", "delete_file"}
	GitWriteTools = []string{"git_create_branch", "git_commit"}
)

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
	add(s, "grep", "Search file contents (git grep): returns path:line:text for each match. Use it to find where a symbol, API or string is used.",
		func(ctx context.Context, a grepArgs) (string, error) {
			return repo.Grep(ctx, a.Pattern, a.Path, a.IgnoreCase, a.Regex)
		})
	add(s, "write_file", "Create a file or replace its whole content.",
		func(_ context.Context, a writeArgs) (string, error) { return repo.WriteFile(a.Path, a.Content) })
	add(s, "edit_file", "Replace one exact, unique occurrence of old_text with new_text in a file. Read the file first; include enough surrounding lines to make old_text unique.",
		func(_ context.Context, a editArgs) (string, error) {
			return repo.EditFile(a.Path, a.OldText, a.NewText)
		})
	add(s, "delete_file", "Delete a file.",
		func(_ context.Context, a fileArgs) (string, error) { return repo.DeleteFile(a.Path) })
	add(s, "git_create_branch", "Create a local branch and switch to it.",
		func(ctx context.Context, a branchArgs) (string, error) { return repo.CreateBranch(ctx, a.Name, a.From) })
	add(s, "git_commit", "Commit locally. Always pass paths: the files the task changed; only they are staged and committed, other staged changes stay out. "+
		"Without paths every change in the working tree is staged and committed. Pushing is left to the user.",
		func(ctx context.Context, a commitArgs) (string, error) { return repo.Commit(ctx, a.Message, a.Paths) })
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

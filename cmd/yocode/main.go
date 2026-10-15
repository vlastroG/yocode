// Command yocode is a local assistant for a project's docs and git repository.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vlastroG/yocode/internal/app"
	"github.com/vlastroG/yocode/internal/commands"
	"github.com/vlastroG/yocode/internal/config"
	"github.com/vlastroG/yocode/internal/mcpclient"
	"github.com/vlastroG/yocode/internal/review"
	"github.com/vlastroG/yocode/internal/tui"
)

const configHelp = `
Configuration (environment; with docker compose set these in .env):
  PROJECT_PATH     host path of the project (mounted at /project)
  DOCS_PATH        optional host path of a separate docs project (mounted at /docs)
  CHAT_MODEL       Ollama chat model (default qwen3.5:9b-q4_K_M)
  SEED             sampling seed for reproducible runs, 0 disables (default 42)
  THINK            true/false to enable/disable model thinking (default: model's own)
  NUM_CTX          chat context window in tokens (default 16384)
  TASK_MODE        subagents (default) or single: how task steps run
  GIT_USER_NAME    author and committer name of task commits (default yocode)
  GIT_USER_EMAIL   author and committer email of task commits (default yocode@localhost)
  EMBED_MODEL      Ollama embedding model (default bge-m3)
  REVIEW_LANGUAGE  language of review reports (default English)
  REVIEW_MAX_FILES files a review may cover; more stops with a warning (default 25)
  REINDEX          true to rebuild the docs index on start
`

func usage(cmds commands.Set) string {
	return "yocode - local assistant for your project's docs and git repository.\n\nUsage: yocode [command]\n" +
		commands.UsageLine("(none)", "start the interactive chat; the commands below work there as /<name>") +
		cmds.CLI().Usage("") +
		commands.UsageLine("help", "show this help") +
		configHelp
}

func main() {
	args := os.Args[1:]
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	cmds := commands.New(commands.Deps{}).CLI() // names, help and argument checks only
	switch name {
	case "help", "-h", "--help":
		fmt.Print(usage(cmds))
		return
	case "", "chat":
	default:
		c, ok := cmds.Lookup(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, usage(cmds))
			os.Exit(2)
		}
		// Check the arguments before the slow setup (models, docs index).
		if err := c.Check(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
	}
	if err := run(name, args); err != nil {
		var tooMany *review.TooManyFilesError
		if errors.As(err, &tooMany) {
			fmt.Fprintln(os.Stderr, "warning:", err)
			os.Exit(3)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(name string, args []string) error {
	ctx := context.Background()
	cfg := config.FromEnv(os.Getenv)
	a, err := app.Setup(ctx, cfg, func(ctx context.Context) (*mcpclient.Client, error) {
		c, err := mcpclient.Start(ctx, cfg.MCPCommand, cfg.ProjectDir)
		if err != nil {
			return nil, fmt.Errorf("start MCP server %s: %w", cfg.MCPCommand, err)
		}
		return c, nil
	})
	if err != nil {
		return err
	}
	defer a.Close()

	if name == "" || name == "chat" {
		return tui.Run(a.Deps(nil), a.Info)
	}
	onTool := func(tool string, args map[string]any) { fmt.Fprintln(os.Stderr, "tool:", tool, args) }
	onProgress := func(s string) { fmt.Fprintln(os.Stderr, s) }
	a.Agent.OnToolCall = onTool
	a.Review.OnProgress = onProgress
	a.Task.Planner.OnToolCall = onTool
	a.Task.Executor.OnToolCall = onTool
	a.Task.Executor.OnProgress = onProgress
	c, _ := commands.New(a.Deps(stdinPrompt(bufio.NewReader(os.Stdin)))).CLI().Lookup(name)
	out, err := c.Run(ctx, args[1:])
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

// stdinPrompt prints text to stderr and reads one line; empty stdin gives io.EOF.
func stdinPrompt(in *bufio.Reader) func(string) (string, error) {
	return func(text string) (string, error) {
		fmt.Fprint(os.Stderr, text)
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
}

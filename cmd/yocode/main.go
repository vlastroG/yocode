// Command yocode is a local assistant for a project's docs and git repository.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/philippgille/chromem-go"
	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/commands"
	"github.com/vlastroG/yocode/internal/config"
	"github.com/vlastroG/yocode/internal/docs"
	"github.com/vlastroG/yocode/internal/mcpclient"
	"github.com/vlastroG/yocode/internal/mcpserver"
	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/rag"
	"github.com/vlastroG/yocode/internal/review"
	"github.com/vlastroG/yocode/internal/task"
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
	app, err := setup(ctx, config.FromEnv(os.Getenv))
	if err != nil {
		return err
	}
	defer app.close()

	d := commands.Deps{Agent: app.agent, Review: app.review, Task: app.task}
	if name == "" || name == "chat" {
		return tui.Run(d, app.info)
	}
	onTool := func(tool string, a map[string]any) { fmt.Fprintln(os.Stderr, "tool:", tool, a) }
	onProgress := func(s string) { fmt.Fprintln(os.Stderr, s) }
	app.agent.OnToolCall = onTool
	app.review.OnProgress = onProgress
	app.task.Planner.OnToolCall = onTool
	app.task.Executor.OnToolCall = onTool
	app.task.Executor.OnProgress = onProgress
	d.Prompt = stdinPrompt(bufio.NewReader(os.Stdin))
	c, _ := commands.New(d).CLI().Lookup(name)
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

type app struct {
	agent  *agent.Agent
	review *review.Pipeline
	task   *task.Session
	info   string
	close  func()
}

// setup pulls missing models, builds or reuses the docs index and starts the MCP server.
func setup(ctx context.Context, cfg config.Config) (*app, error) {
	llm := &ollama.Client{BaseURL: cfg.OllamaURL, NumCtx: cfg.NumCtx, Seed: cfg.Seed, Think: cfg.Think}
	if err := llm.EnsureModels(ctx, os.Stderr, cfg.ChatModel, cfg.EmbedModel); err != nil {
		return nil, fmt.Errorf("ollama at %s: %w", cfg.OllamaURL, err)
	}

	store, err := rag.Open(filepath.Join(cfg.DataDir, "vectors"),
		chromem.NewEmbeddingFuncOllama(cfg.EmbedModel, cfg.OllamaURL+"/api"))
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(os.Stderr, "preparing docs index...")
	idx, built, err := store.Ensure(ctx, cfg.IndexKey, cfg.Reindex, func() ([]docs.Chunk, error) {
		return docs.LoadChunks(cfg.ProjectDir, cfg.DocsDir)
	})
	if err != nil {
		return nil, err
	}
	state := "reused"
	if built {
		state = "built"
	}
	info := fmt.Sprintf("docs index %s: %s (%d chunks) · model %s · project %s",
		cfg.IndexKey, state, idx.Count(), cfg.ChatModel, cfg.ProjectDir)
	fmt.Fprintln(os.Stderr, info)

	tools, err := mcpclient.Start(ctx, cfg.MCPCommand, cfg.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("start MCP server %s: %w", cfg.MCPCommand, err)
	}
	readOnly := tools.Only(mcpserver.ReadTools...)
	edit := append(append([]string(nil), mcpserver.ReadTools...), mcpserver.EditTools...)
	gitWrite := append(append([]string(nil), edit...), mcpserver.GitWriteTools...)
	views := map[task.Access]agent.Tools{
		task.AccessRead: readOnly,
		task.AccessEdit: tools.Only(edit...),
		task.AccessGit:  tools.Only(gitWrite...),
	}
	return &app{
		agent: &agent.Agent{LLM: llm, Model: cfg.ChatModel, Tools: readOnly, Docs: idx},
		review: &review.Pipeline{LLM: llm, Model: cfg.ChatModel, Tools: readOnly, Docs: idx,
			Language: cfg.ReviewLanguage, MaxFiles: cfg.ReviewMaxFiles},
		task: &task.Session{
			Planner: &task.Planner{LLM: llm, Model: cfg.ChatModel, Tools: readOnly, Docs: idx},
			Executor: &task.Executor{LLM: llm, Model: cfg.ChatModel, Single: cfg.TaskSingle,
				ToolsFor: func(a task.Access) agent.Tools {
					if v, ok := views[a]; ok {
						return v
					}
					return readOnly // a hand-edited replay plan may name an unknown access
				},
				Status: tools.Only("git_status")},
			Store: task.Store{Dir: filepath.Join(cfg.DataDir, "tasks"), Now: time.Now},
		},
		info:  info,
		close: func() { tools.Close() },
	}, nil
}

// Command yocode is a local, read-only assistant for a project's docs and git repository.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/philippgille/chromem-go"
	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/commands"
	"github.com/vlastroG/yocode/internal/config"
	"github.com/vlastroG/yocode/internal/docs"
	"github.com/vlastroG/yocode/internal/mcpclient"
	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/rag"
	"github.com/vlastroG/yocode/internal/review"
	"github.com/vlastroG/yocode/internal/tui"
)

const configHelp = `
Configuration (environment; with docker compose set these in .env):
  PROJECT_PATH     host path of the project (mounted read-only at /project)
  DOCS_PATH        optional host path of a separate docs project (mounted at /docs)
  CHAT_MODEL       Ollama chat model (default qwen2.5:7b)
  EMBED_MODEL      Ollama embedding model (default bge-m3)
  REVIEW_LANGUAGE  language of review reports (default English)
  REVIEW_MAX_FILES files a review may cover; more stops with a warning (default 25)
  REINDEX          true to rebuild the docs index on start
`

func usage(cmds commands.Set) string {
	return "yocode - local, read-only assistant for your project's docs and git repository.\n\nUsage: yocode [command]\n" +
		commands.UsageLine("(none)", "start the interactive chat; the commands below work there as /<name>") +
		cmds.Usage("") +
		commands.UsageLine("help", "show this help") +
		configHelp
}

func main() {
	args := os.Args[1:]
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	cmds := commands.New(nil, nil) // names, help and argument checks only
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

	if name == "" || name == "chat" {
		return tui.Run(app.agent, app.review, app.info)
	}
	app.agent.OnToolCall = func(tool string, a map[string]any) { fmt.Fprintln(os.Stderr, "tool:", tool, a) }
	app.review.OnProgress = func(s string) { fmt.Fprintln(os.Stderr, s) }
	c, _ := commands.New(app.agent, app.review).Lookup(name)
	out, err := c.Run(ctx, args[1:])
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

type app struct {
	agent  *agent.Agent
	review *review.Pipeline
	info   string
	close  func()
}

// setup pulls missing models, builds or reuses the docs index and starts the MCP server.
func setup(ctx context.Context, cfg config.Config) (*app, error) {
	llm := &ollama.Client{BaseURL: cfg.OllamaURL, NumCtx: cfg.NumCtx}
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
	return &app{
		agent: &agent.Agent{LLM: llm, Model: cfg.ChatModel, Tools: tools, Docs: idx},
		review: &review.Pipeline{LLM: llm, Model: cfg.ChatModel, Tools: tools, Docs: idx,
			Language: cfg.ReviewLanguage, MaxFiles: cfg.ReviewMaxFiles},
		info:  info,
		close: func() { tools.Close() },
	}, nil
}

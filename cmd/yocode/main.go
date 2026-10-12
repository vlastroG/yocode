// Command yocode is a local, read-only assistant for a project's docs and git repository.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/philippgille/chromem-go"
	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/config"
	"github.com/vlastroG/yocode/internal/docs"
	"github.com/vlastroG/yocode/internal/mcpclient"
	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/rag"
	"github.com/vlastroG/yocode/internal/tui"
)

const usage = `yocode - local, read-only assistant for your project's docs and git repository.

Usage:
  yocode              start the interactive chat (default)
  yocode ask "..."    ask one question and print the answer
  yocode help         show this help

Configuration (environment; with docker compose set these in .env):
  PROJECT_PATH   host path of the project (mounted read-only at /project)
  DOCS_PATH      optional host path of a separate docs project (mounted at /docs)
  CHAT_MODEL     Ollama chat model (default qwen2.5:7b)
  EMBED_MODEL    Ollama embedding model (default bge-m3)
  REINDEX        true to rebuild the docs index on start
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	case "", "chat", "ask":
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err := run(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	ctx := context.Background()
	cfg := config.FromEnv(os.Getenv)

	var question string
	if cmd == "ask" {
		question = strings.TrimSpace(strings.Join(args[1:], " "))
		if question == "" {
			return errors.New(`usage: yocode ask "question"`)
		}
	}

	a, info, closeTools, err := setup(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeTools()

	if cmd != "ask" {
		return tui.Run(a, info)
	}
	a.OnToolCall = func(name string, args map[string]any) { fmt.Fprintln(os.Stderr, "tool:", name, args) }
	answer, err := a.Ask(ctx, question)
	if err != nil {
		return err
	}
	fmt.Println(answer)
	return nil
}

// setup pulls missing models, builds or reuses the docs index and starts the MCP server.
func setup(ctx context.Context, cfg config.Config) (*agent.Agent, string, func(), error) {
	llm := &ollama.Client{BaseURL: cfg.OllamaURL, NumCtx: cfg.NumCtx}
	if err := llm.EnsureModels(ctx, os.Stderr, cfg.ChatModel, cfg.EmbedModel); err != nil {
		return nil, "", nil, fmt.Errorf("ollama at %s: %w", cfg.OllamaURL, err)
	}

	store, err := rag.Open(filepath.Join(cfg.DataDir, "vectors"),
		chromem.NewEmbeddingFuncOllama(cfg.EmbedModel, cfg.OllamaURL+"/api"))
	if err != nil {
		return nil, "", nil, err
	}
	fmt.Fprintln(os.Stderr, "preparing docs index...")
	idx, built, err := store.Ensure(ctx, cfg.IndexKey, cfg.Reindex, func() ([]docs.Chunk, error) {
		return docs.LoadChunks(cfg.ProjectDir, cfg.DocsDir)
	})
	if err != nil {
		return nil, "", nil, err
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
		return nil, "", nil, fmt.Errorf("start MCP server %s: %w", cfg.MCPCommand, err)
	}
	a := &agent.Agent{LLM: llm, Model: cfg.ChatModel, Tools: tools, Docs: idx}
	return a, info, func() { tools.Close() }, nil
}

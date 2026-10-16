// Package app wires yocode's parts (DeepSeek, local embeddings and docs index, MCP tools) for the CLI, the chat and tests.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/philippgille/chromem-go"
	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/commands"
	"github.com/vlastroG/yocode/internal/config"
	"github.com/vlastroG/yocode/internal/deepseek"
	"github.com/vlastroG/yocode/internal/docs"
	"github.com/vlastroG/yocode/internal/jev"
	"github.com/vlastroG/yocode/internal/mcpclient"
	"github.com/vlastroG/yocode/internal/mcpserver"
	"github.com/vlastroG/yocode/internal/rag"
	"github.com/vlastroG/yocode/internal/review"
	"github.com/vlastroG/yocode/internal/task"
)

type App struct {
	Agent  *agent.Agent
	Review *review.Pipeline
	Task   *task.Session
	Tools  *mcpclient.Client
	Info   string

	readOnly agent.Tools
	router   commands.Router
}

// Setup pulls the embedding model if missing, builds or reuses the docs index and wires everything to the MCP
// client that connect returns. main passes mcpclient.Start of the yocode-mcp binary; tests pass an
// in-memory server.
func Setup(ctx context.Context, cfg config.Config, connect func(context.Context) (*mcpclient.Client, error)) (*App, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("DEEPSEEK_API_KEY is not set (put it in .env)")
	}
	llm := &deepseek.Client{BaseURL: cfg.APIURL, APIKey: cfg.APIKey, Retries: 3}
	if err := rag.EnsureModel(ctx, cfg.OllamaURL, cfg.EmbedModel, os.Stderr); err != nil {
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
	var docsSearch agent.Retriever = idx
	var router commands.Router
	if cfg.JevKey != "" {
		j := &jev.Client{APIKey: cfg.JevKey, Model: cfg.JevModel, Retries: 2}
		var warned sync.Once
		docsSearch = &jev.Reranker{Inner: idx, Jev: j, OnError: func(err error) {
			warned.Do(func() { fmt.Fprintln(os.Stderr, "jev unavailable, using plain docs search:", err) })
		}}
		router = j.PlanReply
		info += " · Jev " + cfg.JevModel
	}
	fmt.Fprintln(os.Stderr, info)

	tools, err := connect(ctx)
	if err != nil {
		return nil, err
	}
	readOnly := tools.Only(mcpserver.ReadTools...)
	edit := append(append([]string(nil), mcpserver.ReadTools...), mcpserver.EditTools...)
	gitWrite := append(append([]string(nil), edit...), mcpserver.GitWriteTools...)
	views := map[task.Access]agent.Tools{
		task.AccessRead: readOnly,
		task.AccessEdit: tools.Only(edit...),
		task.AccessGit:  tools.Only(gitWrite...),
	}
	return &App{
		Agent: &agent.Agent{LLM: llm, Model: cfg.ChatModel, Tools: readOnly, Docs: docsSearch},
		Review: &review.Pipeline{LLM: llm, Model: cfg.ChatModel, Tools: readOnly, Docs: docsSearch,
			Language: cfg.ReviewLanguage, MaxFiles: cfg.ReviewMaxFiles},
		Task: &task.Session{
			Planner: &task.Planner{LLM: llm, Model: cfg.ChatModel, Tools: readOnly, Docs: docsSearch},
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
		Tools:    tools,
		Info:     info,
		readOnly: readOnly,
		router:   router,
	}, nil
}

// Deps binds the commands to the app; prompt asks on the terminal (nil in the chat).
func (a *App) Deps(prompt func(string) (string, error)) commands.Deps {
	return commands.Deps{Agent: a.Agent, Review: a.Review, Task: a.Task, Tools: a.readOnly, Prompt: prompt,
		Router: a.router}
}

func (a *App) Close() { a.Tools.Close() }

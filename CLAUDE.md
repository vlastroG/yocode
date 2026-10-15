# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

- Unit tests (no Docker or Ollama needed): `go test ./...`
- Single test: `go test ./internal/agent -run '^TestToolCallLoop$'`
- Integration scenarios (real model on a fixture repo, `internal/integration`, build tag `integration`): `docker compose run --rm itest`; one scenario: `docker compose run --rm --entrypoint go itest test -tags integration -v -timeout 90m -run '^TestTaskFindUsages$' ./internal/integration/`
- Build/vet: `go build ./... && go vet ./...`
- Run everything: set `PROJECT_PATH` in `.env`, then `docker compose run --rm yocode` (TUI, needs a real terminal) or `docker compose run --rm -T yocode ask "..."` (works without a TTY)
- Rebuild the image after Go changes: `docker compose build yocode`
- Ollama serves only the embedding model (bge-m3) for the docs index; the chat model is DeepSeek (`DEEPSEEK_API_KEY`, `CHAT_MODEL` default `deepseek-flash`, client in `internal/deepseek`). GPU: `compose.yaml` runs Ollama on the CPU (portable, e.g. Mac); `compose.gpu.yaml` adds the NVIDIA reservation and is enabled through `COMPOSE_FILE` in `.env`. Check with `docker compose exec ollama ollama ps` (`100% GPU`). After changing compose files, recreate the container: `docker compose up -d --force-recreate ollama`.
- `OLLAMA_URL` in `.env` points the embeddings at a native Ollama (e.g. `http://host.docker.internal:11434` on a Mac); run with `--no-deps`.

## Architecture

yocode is a POC: a CLI in the `yocode` container talks to the DeepSeek API (`internal/deepseek`, OpenAI-format chat completions with tool calls; tool loops run with thinking off and temperature 0, `ChatJSON` uses JSON mode with the schema in the prompt and thinking on) and to Ollama in the `ollama` container for embeddings only (CPU by default, NVIDIA GPU via `compose.gpu.yaml`, or a native Ollama via `OLLAMA_URL`). Everything the tools read is sent to DeepSeek.

- `internal/app.Setup` (called from `cmd/yocode/main.go`) is the wiring point: it pulls the embedding model if missing, builds or reuses the docs index, and spawns `yocode-mcp` over stdio.
- Docs RAG: `internal/docs` (discover + heading-aware chunking) → `internal/rag` (chromem-go, persisted under `/data/vectors`). The collection name is `config.IndexKey` = hash(index layout version, project host path, docs host path, embedding model), so a known project reuses its embeddings; `REINDEX=true` rebuilds.
- Tools: `internal/gittools` (git and file reads, path-escape and option-injection guards) is exposed by `internal/mcpserver` (go-sdk MCP) through the `cmd/yocode-mcp` binary. It has read tools (incl. `grep`), edit tools (`write_file`, `edit_file`, `delete_file`) and local git-write tools (`git_create_branch`, `git_commit`); there are no remote operations (no pull/push/fetch), the user does those on the host. `internal/mcpclient` converts MCP tools to the OpenAI function-calling format; `Client.Only(names...)` restricts a caller to a subset (`mcpserver.ReadTools`, `EditTools`, `GitWriteTools`): `ask`/`review` stay read-only.
- `internal/agent`: each question gets the top 5 doc chunks injected, then a loop of up to 6 `/api/chat` calls that execute tool calls. History keeps only plain question/answer pairs.
- `internal/commands`: the single registry of actions (`ask`, `review`, ...) with argument syntax, help, a cheap `Check` and `Run`. The CLI (`yocode <name> args`, which is also how the container is started with a command) and the TUI (`/<name> args`, plain text = `ask`, or `revise` while a plan is pending) both use it; add new commands there.
- `internal/review`: a fixed review pipeline, not the agent loop. It lists changes via the MCP `git_changed_files` tool, then for each file fetches its diff (lines numbered with new-file line numbers, split at hunks at ≤ 8000 chars), adds 3 RAG doc chunks, and gets findings as JSON following a schema (`deepseek.ChatJSON`). One summary call adds cross-file findings. The `review` command adds the report to the agent history via `Agent.Remember`.
- `internal/task`: the `task` command. Planning (read-only exploration, then a JSON-schema plan of steps with `access` read|edit|git), plan revision, then execution: one loop for all steps with the widest step access (`TASK_MODE=single`, default) or one subagent `agent.Loop` per step with its own access (`subagents`), a root summary, and plans/results saved under `/data/tasks`. A failed step stops the run.
- `diff [path]` command: `git diff HEAD` plus untracked files via the read-only tools; the TUI colors it.
- `internal/jev` (optional, `TYPESAFE_API_KEY`): TypeSafe Jev over HTTP. `jev.Reranker` wraps the docs index for ask/review/planner: a 20-chunk shortlist, one Noul per chunk in one request, chunks below 0.5 dropped; it falls back to plain search on errors. `PlanReply` is a Choice (revise/approve/cancel/ask) used by the hidden `reply` command, which `Parse` picks for plain text while a plan is pending; `commands/reply.go` holds the confidence policy (ask ≥ 0.6, approve/cancel ≥ 0.85, else revise).
- `internal/tui`: Bubble Tea; `agent.OnToolCall` streams tool calls into the UI.

The project is mounted writable (no `:ro`; tasks edit files) and git runs with `GIT_OPTIONAL_LOCKS=0`. The Dockerfile sets system git config (`safe.directory`, `autocrlf`, `fileMode`) so Windows checkouts don't show up as modified. Both Dockerfile stages use Debian bases, not Alpine: Docker Desktop's DNS returns AAAA answers that musl's resolver rejects, which breaks downloads during the build.

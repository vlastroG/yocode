# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

- Unit tests (no Docker or Ollama needed): `go test ./...`
- Single test: `go test ./internal/agent -run '^TestToolCallLoop$'`
- Build/vet: `go build ./... && go vet ./...`
- Run everything: set `PROJECT_PATH` in `.env`, then `docker compose run --rm yocode` (TUI, needs a real terminal) or `docker compose run --rm -T yocode ask "..."` (works without a TTY)
- Rebuild the image after Go changes: `docker compose build yocode`
- GPU: `compose.yaml` runs Ollama on the CPU (portable, e.g. Mac); `compose.gpu.yaml` adds the NVIDIA reservation and is enabled through `COMPOSE_FILE` in `.env`. Check with `docker compose exec ollama ollama ps` (`100% GPU`). After changing compose files, recreate the container: `docker compose up -d --force-recreate ollama`.
- `OLLAMA_URL` in `.env` points yocode at a native Ollama (e.g. `http://host.docker.internal:11434` on a Mac); run with `--no-deps`.

## Architecture

yocode is a read-only POC: a CLI in the `yocode` container talks to Ollama in the `ollama` container (CPU by default, NVIDIA GPU via `compose.gpu.yaml`) or to a native Ollama via `OLLAMA_URL`.

- `cmd/yocode/main.go` `setup()` is the wiring point: it pulls missing models, builds or reuses the docs index, and spawns `yocode-mcp` over stdio.
- Docs RAG: `internal/docs` (discover + heading-aware chunking) → `internal/rag` (chromem-go, persisted under `/data/vectors`). The collection name is `config.IndexKey` = hash(project host path, docs host path, embedding model), so a known project reuses its embeddings; `REINDEX=true` rebuilds.
- Tools: `internal/gittools` (git and file reads, path-escape and option-injection guards) is exposed by `internal/mcpserver` (go-sdk MCP) through the `cmd/yocode-mcp` binary. `internal/mcpclient` converts MCP tools to Ollama's function-calling format.
- `internal/agent`: each question gets the top 5 doc chunks injected, then a loop of up to 6 `/api/chat` calls that execute tool calls. History keeps only plain question/answer pairs.
- `internal/commands`: the single registry of actions (`ask`, `review`, ...) with argument syntax, help, a cheap `Check` and `Run`. The CLI (`yocode <name> args`, which is also how the container is started with a command) and the TUI (`/<name> args`, plain text = `ask`) both use it; add new commands there.
- `internal/review`: a fixed review pipeline, not the agent loop. It lists changes via the MCP `git_changed_files` tool, then for each file fetches its diff (lines numbered with new-file line numbers, split at hunks at ≤ 8000 chars), adds 3 RAG doc chunks, and gets findings from Ollama constrained to a JSON schema (`ollama.ChatJSON`). One summary call adds cross-file findings. The `review` command adds the report to the agent history via `Agent.Remember`.
- `internal/tui`: Bubble Tea; `agent.OnToolCall` streams tool calls into the UI.

The project is mounted `:ro` and git runs with `GIT_OPTIONAL_LOCKS=0`. The Dockerfile sets system git config (`safe.directory`, `autocrlf`, `fileMode`) so Windows checkouts don't show up as modified. Both Dockerfile stages use Debian bases, not Alpine: Docker Desktop's DNS returns AAAA answers that musl's resolver rejects, which breaks downloads during the build.

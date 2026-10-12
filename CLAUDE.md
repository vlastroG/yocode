# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

- Unit tests (no Docker or Ollama needed): `go test ./...`
- Single test: `go test ./internal/agent -run '^TestToolCallLoop$'`
- Build/vet: `go build ./... && go vet ./...`
- Run everything: set `PROJECT_PATH` in `.env`, then `docker compose run --rm yocode` (TUI, needs a real terminal) or `docker compose run --rm -T yocode ask "..."` (works without a TTY)
- Rebuild the image after Go changes: `docker compose build yocode`
- GPU check: `docker compose exec ollama ollama ps` should show `100% GPU`

## Architecture

yocode is a read-only POC: a CLI in the `yocode` container talks to Ollama in the `ollama` container (GPU).

- `cmd/yocode/main.go` `setup()` is the wiring point: it pulls missing models, builds or reuses the docs index, and spawns `yocode-mcp` over stdio.
- Docs RAG: `internal/docs` (discover + heading-aware chunking) → `internal/rag` (chromem-go, persisted under `/data/vectors`). The collection name is `config.IndexKey` = hash(project host path, docs host path, embedding model), so a known project reuses its embeddings; `REINDEX=true` rebuilds.
- Tools: `internal/gittools` (git and file reads, path-escape and option-injection guards) is exposed by `internal/mcpserver` (go-sdk MCP) through the `cmd/yocode-mcp` binary. `internal/mcpclient` converts MCP tools to Ollama's function-calling format.
- `internal/agent`: each question gets the top 5 doc chunks injected, then a loop of up to 6 `/api/chat` calls that execute tool calls. History keeps only plain question/answer pairs.
- `internal/tui`: Bubble Tea; `agent.OnToolCall` streams tool calls into the UI.

The project is mounted `:ro` and git runs with `GIT_OPTIONAL_LOCKS=0`. The Dockerfile sets system git config (`safe.directory`, `autocrlf`, `fileMode`) so Windows checkouts don't show up as modified. Both Dockerfile stages use Debian bases, not Alpine: Docker Desktop's DNS returns AAAA answers that musl's resolver rejects, which breaks downloads during the build.

# yocode

A local, read-only code assistant. It answers questions about a project using its documentation (RAG) and read-only git/file tools exposed through an MCP server. Everything runs in Docker with Ollama; nothing leaves your machine.

## Requirements

- Docker Desktop (WSL2 backend) with an NVIDIA GPU (8 GB VRAM is enough for the default model)

## Usage

1. Copy `.env.example` to `.env` and set `PROJECT_PATH` (and optionally `DOCS_PATH` for a separate docs project, e.g. a Hugo site).
2. Start a chat:

   ```
   docker compose run --rm yocode
   ```

   One-shot question: `docker compose run --rm yocode ask "How is the project structured?"`

The chat needs a real terminal: use PowerShell or Windows Terminal (in Git Bash's mintty, prefix the command with `winpty`). Without a terminal, use `ask` with `-T`: `docker compose run --rm -T yocode ask "..."`.

The first run downloads the models (about 5 GB) and builds the docs index. Later runs reuse both. Set `REINDEX=true` in `.env` (or pass `-e REINDEX=true`) to rebuild the index after the docs change. Stop Ollama with `docker compose down`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PROJECT_PATH` | required | Project to work with, mounted read-only |
| `DOCS_PATH` | none | Separate docs project |
| `CHAT_MODEL` | `qwen2.5:7b` | Ollama chat model (must support tool calling) |
| `EMBED_MODEL` | `qwen3-embedding:0.6b` | Ollama embedding model (multilingual; handles Russian docs) |
| `REINDEX` | `false` | Rebuild the docs index on start |

## Tools available to the assistant

`git_status`, `git_log`, `git_diff`, `git_branch`, `list_files`, `read_file`, all read-only.

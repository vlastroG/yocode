# yocode

A local, read-only code assistant. It answers questions about a project using its documentation (RAG) and read-only git/file tools exposed through an MCP server. Everything runs in Docker with Ollama; nothing leaves your machine.

## Requirements

Docker Desktop (or Docker Engine on Linux) with about 8 GB of memory available to containers for the default models.

| Platform | Setup | Speed |
|---|---|---|
| Windows / Linux with an NVIDIA GPU (8 GB VRAM is enough) | Enable `compose.gpu.yaml` via the `COMPOSE_FILE` lines in `.env` | ~5 s per answer |
| Any machine, CPU only (incl. Mac in Docker) | Nothing to do: Ollama runs on the CPU by default | ~30 s per answer |
| Mac, Apple GPU | Install [Ollama](https://ollama.com) natively, set `OLLAMA_URL=http://host.docker.internal:11434` and run with `--no-deps` | fast (untested) |

Docker can't pass the Apple GPU into containers, which is why the native Ollama setup exists. On a Mac, check that Docker Desktop's memory limit (Settings → Resources) is at least 8 GB when running Ollama in Docker.

## Usage

1. Copy `.env.example` to `.env` and set `PROJECT_PATH` (and optionally `DOCS_PATH` for a separate docs project, e.g. a Hugo site).
2. Start a chat:

   ```
   docker compose run --rm yocode
   ```

   One-shot question: `docker compose run --rm yocode ask "How is the project structured?"`

   With a native Ollama (`OLLAMA_URL` set), add `--no-deps` so the bundled Ollama container isn't started: `docker compose run --rm --no-deps yocode`

The chat needs a real terminal: use PowerShell or Windows Terminal (in Git Bash's mintty, prefix the command with `winpty`). Without a terminal, use `ask` with `-T`: `docker compose run --rm -T yocode ask "..."`.

The first run downloads the models (about 5 GB) and builds the docs index. Later runs reuse both. Set `REINDEX=true` in `.env` (or pass `-e REINDEX=true`) to rebuild the index after the docs change. Stop Ollama with `docker compose down`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PROJECT_PATH` | required | Project to work with, mounted read-only |
| `DOCS_PATH` | none | Separate docs project |
| `CHAT_MODEL` | `qwen2.5:7b` | Ollama chat model (must support tool calling) |
| `EMBED_MODEL` | `bge-m3` | Ollama embedding model (multilingual; handles Russian docs) |
| `REINDEX` | `false` | Rebuild the docs index on start |
| `OLLAMA_URL` | `http://ollama:11434` | Ollama to use; point it at a native install to bypass the bundled container |
| `COMPOSE_FILE` | `compose.yaml` | Set to `compose.yaml,compose.gpu.yaml` (with `COMPOSE_PATH_SEPARATOR=,`) for an NVIDIA GPU |

## Tools available to the assistant

`git_status`, `git_log`, `git_diff`, `git_branch`, `list_files`, `read_file`, all read-only.

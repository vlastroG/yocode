# yocode

A local code assistant. It answers questions about a project using its documentation (RAG, hybrid semantic + keyword search) and git/file tools exposed through an MCP server, and carries out high-level tasks by editing files and committing locally. Everything runs in Docker with Ollama; nothing leaves your machine.

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
2. Start yocode. Without a command you get the interactive chat; with a command the container runs it and exits:

   | Command | What it does |
   |---|---|
   | `docker compose run --rm yocode` | Interactive chat. Type questions, or `/review <base> [head]`, `/task <goal>`, `/diff [path]`, `/help`, `/exit`; Tab completes commands |
   | `docker compose run --rm yocode ask "How is the project structured?"` | Answer one question |
   | `docker compose run --rm yocode review main feature` | Review a branch and print the report |
   | `docker compose run --rm yocode task "add docs to public APIs"` | Plan a goal, correct the plan, run it (see Tasks) |
   | `docker compose run --rm yocode diff` | Show uncommitted changes, including new files |
   | `docker compose run --rm --no-deps yocode help` | List commands and settings (doesn't start Ollama) |

   With a native Ollama (`OLLAMA_URL` set), add `--no-deps` so the bundled Ollama container isn't started: `docker compose run --rm --no-deps yocode`

The chat needs a real terminal: use PowerShell or Windows Terminal (in Git Bash's mintty, prefix the command with `winpty`). Without a terminal, run a command with `-T`: `docker compose run --rm -T yocode ask "..."`.

The first run downloads the models (about 5 GB) and builds the docs index. Later runs reuse both. Set `REINDEX=true` in `.env` (or pass `-e REINDEX=true`) to rebuild the index after the docs change. Stop Ollama with `docker compose down`.

## Reviewing a branch

`review <base> [head]` reviews the changes of `head` (default `HEAD`) since it branched off `base`, like a pull request. Each changed file's diff is reviewed together with related project docs, then a summary pass looks for cross-file problems. The report lists bugs, security problems, architecture problems, documentation that needs updating and recommendations, with `file:line` locations. In the chat (`/review ...`) you can ask follow-up questions about the report.

The branches must exist in your local clone: run `git fetch` on the host first, yocode never contacts remotes. Lockfiles, binaries, vendored and deleted files are skipped. If more than `REVIEW_MAX_FILES` files (default 25) would be reviewed, nothing is reviewed and you get a warning with the file count: each file takes at least one model call, so either rerun with `--max-files N` (e.g. `/review main feature --max-files 80`) or skip the review. Set `REVIEW_LANGUAGE` to get the report in another language.

## Tasks

`task <goal>` (chat: `/task <goal>`) explores the project and proposes a numbered plan. Each step has an access level: `read`, `edit`, or `git` (edit plus local branch and commit).

- Correct the plan by typing in the chat (or any text at the CLI prompt); the plan is revised, repeat until it fits.
- `/go` (CLI: Enter) runs it, `/cancel` (CLI: `q`) drops it. `--yes` skips the prompt.
- All steps run in one conversation with the access of the plan's widest step; the final answer summarizes the result. `TASK_MODE=subagents` runs every step in its own subagent with that step's access instead (fewer scenarios passed in our tests).
- Approved plans are saved as `/data/tasks/<id>.json` and results as `<id>.md` in the `yocode-data` volume. `task --plan <id>` replays a saved plan; `SEED` makes runs reproducible.
- Review the changes with `/diff` (or `yocode diff`). The project is mounted writable, and there are no pull, push or fetch tools: do those manually on the host.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PROJECT_PATH` | required | Project to work with, mounted writable |
| `DOCS_PATH` | none | Separate docs project |
| `CHAT_MODEL` | `qwen3.5:9b-q4_K_M` | Ollama chat model (must support tool calling) |
| `NUM_CTX` | `16384` | Chat context window in tokens |
| `THINK` | `false` | Model thinking on/off |
| `SEED` | `42` | Sampling seed (temperature 0); `0` disables |
| `TASK_MODE` | `single` | `single` (one conversation, widest step access) or `subagents` (one agent per step, per-step access) |
| `GIT_USER_NAME` / `GIT_USER_EMAIL` | `yocode` / `yocode@localhost` | Author and committer of task commits |
| `EMBED_MODEL` | `bge-m3` | Ollama embedding model (multilingual; handles Russian docs) |
| `REINDEX` | `false` | Rebuild the docs index on start |
| `REVIEW_LANGUAGE` | `English` | Language of review reports |
| `REVIEW_MAX_FILES` | `25` | Files a review may cover; above it the review stops with a warning (override per run with `--max-files N`) |
| `OLLAMA_URL` | `http://ollama:11434` | Ollama to use; point it at a native install to bypass the bundled container |
| `COMPOSE_FILE` | `compose.yaml` | Set to `compose.yaml,compose.gpu.yaml` (with `COMPOSE_PATH_SEPARATOR=,`) for an NVIDIA GPU |

## Tools available to the assistant

- Read (`ask`, `review`, `diff`): `git_status`, `git_log`, `git_diff`, `git_changed_files`, `git_branch`, `list_files`, `read_file`, `grep`.
- Edit (tasks): `write_file`, `edit_file`, `delete_file`.
- Local git (tasks, `git` steps): `git_create_branch`, `git_commit`.

## Tests

`go test ./...` runs the unit tests; `docker compose run --rm itest` runs the task scenarios against the real model on a fixture repo.

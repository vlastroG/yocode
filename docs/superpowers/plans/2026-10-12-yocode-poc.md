# yocode POC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A fully local, read-only CLI code assistant (`yocode`) that answers questions about a project using RAG over its docs and a git MCP server, running in Docker Compose with Ollama.

**Architecture:** `docker compose` runs two services: `ollama` (GPU) and `yocode`. On start, `yocode` pulls missing models, builds or reuses a chromem-go vector index of the docs (keyed by host paths + embedding model, persisted in a volume), and launches `yocode-mcp`, a stdio MCP server exposing read-only git and file tools over the project, which is mounted read-only. An agent loop combines the question with the top RAG hits, calls Ollama `/api/chat` with the MCP tools, executes any tool calls, and returns the answer to a small Bubble Tea TUI.

**Tech Stack:** Go 1.24, Ollama (`qwen2.5:7b` chat, `nomic-embed-text` embeddings), `github.com/philippgille/chromem-go` (embedded vector DB), `github.com/modelcontextprotocol/go-sdk` (MCP), `github.com/charmbracelet/bubbletea` + `bubbles` + `lipgloss` (TUI), Docker Compose.

---

## Decisions (read first)

| Topic | Decision | Why |
|---|---|---|
| Hardware | RTX 3060 Ti **8 GB VRAM**, 64 GB RAM, i5-11400F, Docker Desktop on WSL2 | Measured on 2026-10-12 |
| Chat model | `qwen2.5:7b` (≈4.7 GB Q4), `num_ctx` 8192 | Fits in 8 GB VRAM with an 8k context and supports tool calling reliably in Ollama. Override with `CHAT_MODEL` (e.g. `qwen3:8b`, `llama3.1:8b`). |
| Embedding model | `nomic-embed-text` (≈270 MB) | Small and fast, the standard Ollama embedding model |
| Vector DB | chromem-go, persisted to `/data/vectors` in a named volume | Pure Go and embedded: no extra service |
| "Seen before" | Collection name = `docs-` + sha256(project host path, docs host path, embed model). It is reused if non-empty; `REINDEX=true` deletes and rebuilds it. | Covers both requirements with no extra metadata store |
| When indexing runs | On **container start**, not at `docker build` | Embedding needs a running Ollama, which isn't available during an image build. The effect for the user is the same: a single `docker compose run`. |
| MCP transport | `yocode` spawns `yocode-mcp` (same image) over **stdio** | It's real MCP, but there's no extra service or port. Can be moved to a separate HTTP service later. |
| Docs discovery | Every `.md .markdown .mdx .txt .rst .adoc` file and `README*` under `/project` and `/docs`. Skips hidden dirs, `node_modules`, `vendor`, `dist`, `build`, `target`, `bin`, `obj`, `public`, `resources`, `venv`, and files over 1 MB. | Simple and catches README, `docs/` and Hugo `content/` |
| Commands | `yocode` (TUI chat), `yocode ask "..."` (one-shot, used for smoke tests), `yocode help` | `ask` makes end-to-end checks possible without a TTY |
| Safety | `/project` mounted `:ro`; git runs with `GIT_OPTIONAL_LOCKS=0`; refs starting with `-` are rejected; paths can't escape the project root | Read-only MVP |

Not in scope (YAGNI): incremental re-indexing, streaming output, markdown rendering, write tools, non-NVIDIA setups.

## File structure

```
go.mod / go.sum
cmd/yocode/main.go            CLI entry: help | ask | chat; wires everything together
cmd/yocode-mcp/main.go        stdio MCP server entry
internal/config/config.go     env -> Config, index key
internal/docs/discover.go     find doc files under a root
internal/docs/chunk.go        split a doc into heading-aware chunks
internal/docs/load.go         discover + read + split for several roots
internal/ollama/client.go     minimal Ollama HTTP client (chat, tags, pull)
internal/rag/store.go         chromem-go index: ensure/build, search
internal/gittools/repo.go     read-only git/file operations on the project
internal/testutil/gitrepo.go  test helper: temp git repo
internal/mcpserver/server.go  MCP server exposing gittools
internal/mcpclient/client.go  MCP client -> agent tools (Ollama tool format)
internal/agent/agent.go       RAG + tool-calling loop with short history
internal/tui/tui.go           Bubble Tea chat UI
Dockerfile, compose.yaml, .dockerignore, .env.example, .nodocs/.gitkeep
```

Go is installed locally (1.23.3; `GOTOOLCHAIN=auto` downloads a newer one if `go.mod` requires it). All unit tests run on the Windows host with `go test ./...`. Only Ollama runs exclusively in Docker.

**Library API note:** versions are pinned below. If a newer version gets resolved and something doesn't compile, check the symbol with `go doc <package> <Symbol>` and adapt; don't change the design.

---

### Task 0: Branch

- [ ] **Step 1: Create a working branch**

```bash
git checkout -b poc
```

---

### Task 1: Module scaffold + config

**Files:**
- Create: `go.mod`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `.gitignore` (append `.env`)

- [ ] **Step 1: Init module and ignore `.env`**

```bash
go mod init github.com/vlastroG/yocode
printf '\n# local compose settings\n.env\n' >> .gitignore
```

- [ ] **Step 2: Write the failing test** `internal/config/config_test.go`

```go
package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c := FromEnv(env(nil))
	if c.OllamaURL != "http://localhost:11434" {
		t.Errorf("OllamaURL = %q", c.OllamaURL)
	}
	if c.ChatModel != "qwen2.5:7b" || c.EmbedModel != "nomic-embed-text" {
		t.Errorf("models = %q, %q", c.ChatModel, c.EmbedModel)
	}
	if c.ProjectDir != "/project" || c.DocsDir != "/docs" || c.DataDir != "/data" {
		t.Errorf("dirs = %q %q %q", c.ProjectDir, c.DocsDir, c.DataDir)
	}
	if c.MCPCommand != "yocode-mcp" || c.NumCtx != 8192 || c.Reindex {
		t.Errorf("mcp=%q numctx=%d reindex=%v", c.MCPCommand, c.NumCtx, c.Reindex)
	}
	if !strings.HasPrefix(c.IndexKey, "docs-") {
		t.Errorf("IndexKey = %q", c.IndexKey)
	}
}

func TestOverrides(t *testing.T) {
	c := FromEnv(env(map[string]string{
		"OLLAMA_URL": "http://ollama:11434/",
		"CHAT_MODEL": "llama3.1:8b",
		"REINDEX":    "TRUE",
		"NUM_CTX":    "4096",
	}))
	if c.OllamaURL != "http://ollama:11434" {
		t.Errorf("OllamaURL = %q, want trailing slash trimmed", c.OllamaURL)
	}
	if c.ChatModel != "llama3.1:8b" || !c.Reindex || c.NumCtx != 4096 {
		t.Errorf("got %+v", c)
	}
}

func TestIndexKeyNormalizesHostPaths(t *testing.T) {
	a := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": `C:\Repos\Foo\`}))
	b := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": "c:/repos/foo"}))
	if a.IndexKey != b.IndexKey {
		t.Errorf("keys differ: %q vs %q", a.IndexKey, b.IndexKey)
	}
}

func TestIndexKeyDependsOnDocsAndModel(t *testing.T) {
	base := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": "c:/foo"}))
	withDocs := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": "c:/foo", "DOCS_HOST_PATH": "c:/foo-docs"}))
	otherModel := FromEnv(env(map[string]string{"PROJECT_HOST_PATH": "c:/foo", "EMBED_MODEL": "mxbai-embed-large"}))
	if base.IndexKey == withDocs.IndexKey || base.IndexKey == otherModel.IndexKey {
		t.Errorf("keys should differ: %q %q %q", base.IndexKey, withDocs.IndexKey, otherModel.IndexKey)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL with `undefined: FromEnv`

- [ ] **Step 4: Implement** `internal/config/config.go`

```go
// Package config reads yocode settings from the environment.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

type Config struct {
	OllamaURL  string
	ChatModel  string
	EmbedModel string
	NumCtx     int

	ProjectDir string // project mount inside the container
	DocsDir    string // optional separate docs mount inside the container
	DataDir    string // persistent volume for the vector DB
	MCPCommand string // path of the yocode-mcp binary

	// IndexKey identifies the docs index for this (project, docs, embedding model) combination,
	// so a project seen before reuses its embeddings.
	IndexKey string
	Reindex  bool
}

func FromEnv(getenv func(string) string) Config {
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	c := Config{
		OllamaURL:  strings.TrimRight(get("OLLAMA_URL", "http://localhost:11434"), "/"),
		ChatModel:  get("CHAT_MODEL", "qwen2.5:7b"),
		EmbedModel: get("EMBED_MODEL", "nomic-embed-text"),
		NumCtx:     8192,
		ProjectDir: get("PROJECT_DIR", "/project"),
		DocsDir:    get("DOCS_DIR", "/docs"),
		DataDir:    get("DATA_DIR", "/data"),
		MCPCommand: get("YOCODE_MCP", "yocode-mcp"),
		Reindex:    parseBool(getenv("REINDEX")),
	}
	if n, err := strconv.Atoi(getenv("NUM_CTX")); err == nil && n > 0 {
		c.NumCtx = n
	}
	c.IndexKey = indexKey(get("PROJECT_HOST_PATH", c.ProjectDir), getenv("DOCS_HOST_PATH"), c.EmbedModel)
	return c
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func indexKey(project, docs, model string) string {
	h := sha256.Sum256([]byte(normPath(project) + "|" + normPath(docs) + "|" + model))
	return "docs-" + hex.EncodeToString(h[:8])
}

// normPath makes Windows host paths comparable: case-insensitive, forward slashes, no trailing slash.
func normPath(p string) string {
	p = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"))
	return strings.TrimRight(p, "/")
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/config/`
Expected: `ok  github.com/vlastroG/yocode/internal/config`

- [ ] **Step 6: Commit**

```bash
git add go.mod .gitignore internal/config
git commit -m "feat(config): env config and docs index key"
```

---

### Task 2: Doc file discovery

**Files:**
- Create: `internal/docs/discover.go`
- Test: `internal/docs/discover_test.go`

- [ ] **Step 1: Write the failing test** `internal/docs/discover_test.go`

```go
package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverFindsDocsOnly(t *testing.T) {
	root := t.TempDir()
	write(t, root, "README.md", "# Project")
	write(t, root, "docs/guide.md", "# Guide")
	write(t, root, "notes.txt", "notes")
	write(t, root, "main.go", "package main")
	write(t, root, "node_modules/pkg/README.md", "# dep")
	write(t, root, ".github/PULL_REQUEST_TEMPLATE.md", "template")

	files, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Rel)
	}
	want := []string{"README.md", "docs/guide.md", "notes.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	files, err := Discover(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(files) != 0 {
		t.Errorf("got %v, %v; want no files and no error", files, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/docs/`
Expected: FAIL with `undefined: Discover`

- [ ] **Step 3: Implement** `internal/docs/discover.go`

```go
// Package docs finds documentation files and splits them into chunks for indexing.
package docs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxFileSize = 1 << 20

var docExts = map[string]bool{
	".md": true, ".markdown": true, ".mdx": true, ".txt": true, ".rst": true, ".adoc": true,
}

// skipDirs are never documentation (dependencies, build output, Hugo's generated site).
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"bin": true, "obj": true, "public": true, "resources": true, "venv": true,
}

type File struct {
	Rel string // slash-separated, relative to the root
	Abs string
}

// Discover returns documentation files under root. A missing root yields no files.
func Discover(root string) ([]File, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isDoc(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxFileSize {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, File{Rel: filepath.ToSlash(rel), Abs: path})
		return nil
	})
	return files, err
}

func isDoc(name string) bool {
	return docExts[strings.ToLower(filepath.Ext(name))] || strings.HasPrefix(strings.ToUpper(name), "README")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/docs/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/docs
git commit -m "feat(docs): discover documentation files"
```

---

### Task 3: Chunking and loading

**Files:**
- Create: `internal/docs/chunk.go`, `internal/docs/load.go`
- Test: `internal/docs/chunk_test.go`, `internal/docs/load_test.go`

- [ ] **Step 1: Write the failing tests** `internal/docs/chunk_test.go`

```go
package docs

import (
	"strings"
	"testing"
)

func TestSplitByHeadings(t *testing.T) {
	got := Split("a.md", "# Title\nintro\n\n## Install\nrun it\n", 1000)
	want := []Chunk{
		{Source: "a.md", Heading: "Title", Text: "# Title\nintro"},
		{Source: "a.md", Heading: "Install", Text: "## Install\nrun it"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSplitRespectsMaxChars(t *testing.T) {
	content := strings.Repeat(strings.Repeat("x", 30)+"\n", 10)
	got := Split("a.md", content, 100)
	if len(got) < 2 {
		t.Fatalf("expected several chunks, got %d", len(got))
	}
	for _, c := range got {
		if len(c.Text) > 100 {
			t.Errorf("chunk too long: %d", len(c.Text))
		}
	}
}

func TestSplitIgnoresHashInCodeFence(t *testing.T) {
	got := Split("a.md", "## Setup\n```bash\n# install deps\nmake\n```\n", 1000)
	if len(got) != 1 || got[0].Heading != "Setup" {
		t.Errorf("got %+v", got)
	}
}

func TestSplitCRLFAndEmpty(t *testing.T) {
	if got := Split("a.md", "", 100); len(got) != 0 {
		t.Errorf("empty content gave %+v", got)
	}
	got := Split("a.md", "# T\r\nbody\r\n", 100)
	if len(got) != 1 || got[0].Text != "# T\nbody" {
		t.Errorf("got %+v", got)
	}
}

func TestSplitLongLine(t *testing.T) {
	got := Split("a.md", strings.Repeat("y", 250), 100)
	if len(got) != 3 {
		t.Fatalf("got %d chunks", len(got))
	}
	for _, c := range got {
		if len(c.Text) > 100 {
			t.Errorf("chunk too long: %d", len(c.Text))
		}
	}
}
```

`internal/docs/load_test.go`

```go
package docs

import (
	"path/filepath"
	"testing"
)

func TestLoadChunksPrefixesRootName(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	docsDir := filepath.Join(base, "docs")
	write(t, project, "README.md", "# Readme\nhello")
	write(t, docsDir, "content/intro.md", "# Intro\nwelcome")

	chunks, err := LoadChunks(project, docsDir, filepath.Join(base, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	for _, c := range chunks {
		sources[c.Source] = true
	}
	if !sources["project/README.md"] || !sources["docs/content/intro.md"] || len(sources) != 2 {
		t.Errorf("sources = %v", sources)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/docs/`
Expected: FAIL with `undefined: Split`, `undefined: Chunk`, `undefined: LoadChunks`

- [ ] **Step 3: Implement** `internal/docs/chunk.go`

```go
package docs

import "strings"

type Chunk struct {
	Source  string // e.g. "project/docs/install.md"
	Heading string // nearest markdown heading above the chunk
	Text    string
}

// Split cuts content into chunks of at most maxChars bytes (for ASCII text), starting a new
// chunk at every markdown heading outside code fences.
func Split(source, content string, maxChars int) []Chunk {
	var (
		chunks  []Chunk
		buf     strings.Builder
		heading string
		inFence bool
	)
	flush := func() {
		if text := strings.TrimSpace(buf.String()); text != "" {
			chunks = append(chunks, Chunk{Source: source, Heading: heading, Text: text})
		}
		buf.Reset()
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		} else if h, ok := headingText(trimmed); ok && !inFence {
			flush()
			heading = h
		}
		for _, piece := range splitLong(line, maxChars) {
			if buf.Len() > 0 && buf.Len()+len(piece) > maxChars {
				flush()
			}
			buf.WriteString(piece)
			buf.WriteByte('\n')
		}
	}
	flush()
	return chunks
}

func headingText(line string) (string, bool) {
	rest := strings.TrimLeft(line, "#")
	if len(rest) == len(line) || !strings.HasPrefix(rest, " ") {
		return "", false
	}
	t := strings.TrimSpace(rest)
	return t, t != ""
}

func splitLong(line string, max int) []string {
	if len(line) <= max {
		return []string{line}
	}
	var parts []string
	r := []rune(line)
	for len(r) > 0 {
		n := min(max, len(r))
		parts = append(parts, string(r[:n]))
		r = r[n:]
	}
	return parts
}
```

`internal/docs/load.go`

```go
package docs

import (
	"os"
	"path/filepath"
)

// chunkSize keeps chunks well under nomic-embed-text's context (~400 tokens each).
const chunkSize = 1500

// LoadChunks discovers doc files under each root and splits them into chunks.
// Sources are prefixed with the root's base name, e.g. "project/README.md" or "docs/content/intro.md".
func LoadChunks(roots ...string) ([]Chunk, error) {
	var all []Chunk
	for _, root := range roots {
		files, err := Discover(root)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			b, err := os.ReadFile(f.Abs)
			if err != nil {
				return nil, err
			}
			all = append(all, Split(filepath.Base(root)+"/"+f.Rel, string(b), chunkSize)...)
		}
	}
	return all, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/docs/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/docs
git commit -m "feat(docs): heading-aware chunking and multi-root loading"
```

---

### Task 4: Ollama client

**Files:**
- Create: `internal/ollama/client.go`
- Test: `internal/ollama/client_test.go`

- [ ] **Step 1: Write the failing test** `internal/ollama/client_test.go`

```go
package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestChatSendsRequestAndParsesToolCalls(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		io.WriteString(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"git_status","arguments":{}}}]},"done":true}`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, NumCtx: 4096}
	msg, err := c.Chat(context.Background(), "m",
		[]Message{{Role: "user", Content: "hi"}},
		[]Tool{{Type: "function", Function: ToolFunction{Name: "git_status"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "m" || got.Stream || got.Options["num_ctx"] != float64(4096) || len(got.Tools) != 1 {
		t.Errorf("request = %+v", got)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "git_status" {
		t.Errorf("message = %+v", msg)
	}
}

func TestEnsureModelsPullsOnlyMissing(t *testing.T) {
	var pulled []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			io.WriteString(w, `{"models":[{"name":"nomic-embed-text:latest"}]}`)
		case "/api/pull":
			var b struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			pulled = append(pulled, b.Model)
			io.WriteString(w, `{"status":"success"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	if err := c.EnsureModels(context.Background(), io.Discard, "qwen2.5:7b", "nomic-embed-text"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pulled, []string{"qwen2.5:7b"}) {
		t.Errorf("pulled = %v", pulled)
	}
}

func TestErrorStatusIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := (&Client{BaseURL: srv.URL}).Chat(context.Background(), "m", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ollama/`
Expected: FAIL with `undefined: chatRequest` / `undefined: Client`

- [ ] **Step 3: Implement** `internal/ollama/client.go`

```go
// Package ollama is a minimal client for the Ollama HTTP API.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	BaseURL string       // e.g. http://ollama:11434
	NumCtx  int          // context window passed to chat requests; 0 = Ollama default
	HTTP    *http.Client // nil = http.DefaultClient (no timeout: pulls are slow)
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON schema
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Tools    []Tool         `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

func (c *Client) Chat(ctx context.Context, model string, msgs []Message, tools []Tool) (Message, error) {
	req := chatRequest{Model: model, Messages: msgs, Tools: tools}
	if c.NumCtx > 0 {
		req.Options = map[string]any{"num_ctx": c.NumCtx}
	}
	var resp struct {
		Message Message `json:"message"`
	}
	err := c.do(ctx, http.MethodPost, "/api/chat", req, &resp)
	return resp.Message, err
}

func (c *Client) HasModel(ctx context.Context, name string) (bool, error) {
	var resp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/tags", nil, &resp); err != nil {
		return false, err
	}
	for _, m := range resp.Models {
		if m.Name == name || m.Name == name+":latest" {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) Pull(ctx context.Context, name string) error {
	var resp struct {
		Error string `json:"error"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/pull", map[string]any{"model": name, "stream": false}, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

// EnsureModels pulls every model that is not available locally yet.
func (c *Client) EnsureModels(ctx context.Context, log io.Writer, names ...string) error {
	for _, name := range names {
		ok, err := c.HasModel(ctx, name)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		fmt.Fprintf(log, "pulling model %s (first run only, this can take a while)...\n", name)
		if err := c.Pull(ctx, name); err != nil {
			return fmt.Errorf("pull %s: %w", name, err)
		}
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("ollama %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/ollama/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/ollama
git commit -m "feat(ollama): minimal chat/tags/pull client"
```

---

### Task 5: RAG store (chromem-go)

**Files:**
- Create: `internal/rag/store.go`
- Test: `internal/rag/store_test.go`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/philippgille/chromem-go@v0.7.0
```

- [ ] **Step 2: Write the failing test** `internal/rag/store_test.go`

The test uses a deterministic bag-of-words embedding, so it needs no Ollama.

```go
package rag

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/docs"
)

func fakeEmbed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, 64)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%64]++
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	for i := range v {
		v[i] /= float32(math.Sqrt(n))
	}
	return v, nil
}

var chunks = []docs.Chunk{
	{Source: "project/install.md", Heading: "Install", Text: "install with docker compose up"},
	{Source: "project/api.md", Heading: "API", Text: "the http api returns json responses"},
}

func TestEnsureBuildsOnceAndSearches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	s, err := Open(dir, fakeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	ix, built, err := s.Ensure(ctx, "k", false, func() ([]docs.Chunk, error) { return chunks, nil })
	if err != nil || !built || ix.Count() != 2 {
		t.Fatalf("built=%v count=%d err=%v", built, ix.Count(), err)
	}
	hits, err := ix.Search(ctx, "how do I install with docker", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Source != "project/install.md" {
		t.Errorf("hits = %+v", hits)
	}

	// A new process (reopened DB) reuses the persisted index.
	s2, err := Open(dir, fakeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	_, built, err = s2.Ensure(ctx, "k", false, func() ([]docs.Chunk, error) {
		t.Fatal("load must not be called for an existing index")
		return nil, nil
	})
	if err != nil || built {
		t.Fatalf("built=%v err=%v", built, err)
	}

	// Reindex forces a rebuild.
	_, built, err = s2.Ensure(ctx, "k", true, func() ([]docs.Chunk, error) { return chunks, nil })
	if err != nil || !built {
		t.Fatalf("reindex: built=%v err=%v", built, err)
	}
}

func TestSearchEmptyIndex(t *testing.T) {
	s, err := Open(t.TempDir(), fakeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	ix, _, err := s.Ensure(context.Background(), "empty", false, func() ([]docs.Chunk, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	hits, err := ix.Search(context.Background(), "anything", 5)
	if err != nil || len(hits) != 0 {
		t.Errorf("hits=%v err=%v", hits, err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/rag/`
Expected: FAIL with `undefined: Open`

- [ ] **Step 4: Implement** `internal/rag/store.go`

```go
// Package rag stores doc chunk embeddings and retrieves the most relevant ones.
package rag

import (
	"context"
	"fmt"
	"strconv"

	"github.com/philippgille/chromem-go"
	"github.com/vlastroG/yocode/internal/docs"
)

type Store struct {
	db    *chromem.DB
	embed chromem.EmbeddingFunc
}

type Index struct {
	col *chromem.Collection
}

type Hit struct {
	Source  string
	Heading string
	Text    string // includes a "Source: ..." header, ready to paste into a prompt
	Score   float32
}

func Open(dir string, embed chromem.EmbeddingFunc) (*Store, error) {
	db, err := chromem.NewPersistentDB(dir, false)
	if err != nil {
		return nil, fmt.Errorf("open vector db: %w", err)
	}
	return &Store{db: db, embed: embed}, nil
}

// Ensure returns the index named key. It calls load and embeds the chunks when the index is
// empty or reindex is set; built reports whether that happened.
func (s *Store) Ensure(ctx context.Context, key string, reindex bool, load func() ([]docs.Chunk, error)) (ix *Index, built bool, err error) {
	if reindex {
		if err := s.db.DeleteCollection(key); err != nil {
			return nil, false, err
		}
	}
	col, err := s.db.GetOrCreateCollection(key, nil, s.embed)
	if err != nil {
		return nil, false, err
	}
	if col.Count() > 0 {
		return &Index{col: col}, false, nil
	}

	chunks, err := load()
	if err != nil {
		return nil, true, err
	}
	documents := make([]chromem.Document, len(chunks))
	for i, c := range chunks {
		documents[i] = chromem.Document{
			ID:       strconv.Itoa(i),
			Content:  fmt.Sprintf("Source: %s\nSection: %s\n\n%s", c.Source, c.Heading, c.Text),
			Metadata: map[string]string{"source": c.Source, "heading": c.Heading},
		}
	}
	if len(documents) > 0 {
		if err := col.AddDocuments(ctx, documents, 4); err != nil {
			_ = s.db.DeleteCollection(key) // don't leave a half-built index to be reused next time
			return nil, true, fmt.Errorf("embed docs: %w", err)
		}
	}
	return &Index{col: col}, true, nil
}

func (ix *Index) Count() int { return ix.col.Count() }

func (ix *Index) Search(ctx context.Context, query string, n int) ([]Hit, error) {
	n = min(n, ix.col.Count()) // chromem rejects n > number of documents
	if n == 0 {
		return nil, nil
	}
	res, err := ix.col.Query(ctx, query, n, nil, nil)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, len(res))
	for i, r := range res {
		hits[i] = Hit{Source: r.Metadata["source"], Heading: r.Metadata["heading"], Text: r.Content, Score: r.Similarity}
	}
	return hits, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/rag/`
Expected: `ok`

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/rag
git commit -m "feat(rag): persistent chromem index with reuse and reindex"
```

---

### Task 6: Read-only git tools

**Files:**
- Create: `internal/testutil/gitrepo.go`
- Create: `internal/gittools/repo.go`
- Test: `internal/gittools/repo_test.go`

- [ ] **Step 1: Create the test helper** `internal/testutil/gitrepo.go`

```go
// Package testutil holds helpers shared by tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// GitRepo creates a temporary git repository on branch main with one commit ("initial")
// containing README.md = "hello\n".
func GitRepo(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "test")
	run("config", "user.email", "test@example.com")
	run("config", "core.autocrlf", "false")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}
```

- [ ] **Step 2: Write the failing test** `internal/gittools/repo_test.go`

```go
package gittools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/testutil"
)

func TestReadOnlyCommands(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Repo{Dir: dir}

	cases := []struct {
		name string
		run  func() (string, error)
		want string
	}{
		{"status", func() (string, error) { return r.Status(ctx) }, "README.md"},
		{"log", func() (string, error) { return r.Log(ctx, 5, "") }, "initial"},
		{"diff", func() (string, error) { return r.Diff(ctx, "", false, "") }, "+changed"},
		{"branches", func() (string, error) { return r.Branches(ctx) }, "main"},
		{"ls-files", func() (string, error) { return r.ListFiles(ctx, "") }, "README.md"},
		{"read", func() (string, error) { return r.ReadFile("README.md") }, "changed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := c.run()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("output %q does not contain %q", out, c.want)
			}
		})
	}
}

func TestRejectsUnsafeInput(t *testing.T) {
	ctx := context.Background()
	r := Repo{Dir: testutil.GitRepo(t)}

	if _, err := r.ReadFile("../outside.txt"); err == nil {
		t.Error("ReadFile escaped the project")
	}
	if _, err := r.Diff(ctx, "--output=/tmp/x", false, ""); err == nil {
		t.Error("Diff accepted an option as ref")
	}
	if _, err := r.Log(ctx, 5, "../.."); err == nil {
		t.Error("Log accepted a path outside the project")
	}
}

func TestReadFileRejectsBinary(t *testing.T) {
	dir := testutil.GitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Repo{Dir: dir}).ReadFile("bin.dat"); err == nil {
		t.Error("expected error for binary file")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/gittools/`
Expected: FAIL with `undefined: Repo`

- [ ] **Step 4: Implement** `internal/gittools/repo.go`

```go
// Package gittools runs read-only git and file operations inside a project directory.
package gittools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// maxOutput keeps tool results small enough for the model's context window.
const maxOutput = 20000

type Repo struct {
	Dir string
}

func (r Repo) Status(ctx context.Context) (string, error) {
	return r.git(ctx, "status", "--short", "--branch")
}

func (r Repo) Log(ctx context.Context, n int, path string) (string, error) {
	if n <= 0 {
		n = 20
	}
	n = min(n, 200)
	args := []string{"log", "-n", strconv.Itoa(n), "--date=short", "--pretty=format:%h %ad %an: %s"}
	if path != "" {
		p, err := relPath(path)
		if err != nil {
			return "", err
		}
		args = append(args, "--", p)
	}
	return r.git(ctx, args...)
}

func (r Repo) Diff(ctx context.Context, ref string, staged bool, path string) (string, error) {
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\n\x00") {
		return "", fmt.Errorf("invalid ref %q", ref)
	}
	args := []string{"diff"}
	if staged {
		args = append(args, "--staged")
	}
	if ref != "" {
		args = append(args, ref)
	}
	if path != "" {
		p, err := relPath(path)
		if err != nil {
			return "", err
		}
		args = append(args, "--", p)
	}
	return r.git(ctx, args...)
}

func (r Repo) Branches(ctx context.Context) (string, error) {
	return r.git(ctx, "branch", "-a", "-vv")
}

func (r Repo) ListFiles(ctx context.Context, path string) (string, error) {
	args := []string{"ls-files"}
	if path != "" {
		p, err := relPath(path)
		if err != nil {
			return "", err
		}
		args = append(args, "--", p)
	}
	return r.git(ctx, args...)
}

func (r Repo) ReadFile(path string) (string, error) {
	p, err := relPath(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(r.Dir)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(root, full); err != nil || escapes(rel) {
		return "", fmt.Errorf("path %q is outside the project", path)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return "", fmt.Errorf("%s looks like a binary file", path)
	}
	return truncate(string(b)), nil
}

// relPath validates a project-relative path and returns it slash-separated.
func relPath(path string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || escapes(clean) {
		return "", fmt.Errorf("path %q is outside the project", path)
	}
	return filepath.ToSlash(clean), nil
}

func escapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (r Repo) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir, "--no-pager"}, args...)...)
	// No index refresh writes (the project is mounted read-only), never prompt.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		msg := ""
		if errors.As(err, &ee) {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s failed: %v: %s", args[0], err, msg)
	}
	if strings.TrimSpace(string(out)) == "" {
		return "(no output)", nil
	}
	return truncate(string(out)), nil
}

func truncate(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + "\n... [truncated]"
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/gittools/`
Expected: `ok`

- [ ] **Step 6: Commit**

```bash
git add internal/testutil internal/gittools
git commit -m "feat(gittools): read-only git and file operations"
```

---

### Task 7: MCP server + client

**Files:**
- Create: `internal/mcpserver/server.go`
- Create: `cmd/yocode-mcp/main.go`
- Create: `internal/mcpclient/client.go`
- Test: `internal/mcpclient/client_test.go`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.0.0
```

- [ ] **Step 2: Write the failing test** `internal/mcpclient/client_test.go`

It connects the real server and client over the SDK's in-memory transport.

```go
package mcpclient

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/gittools"
	"github.com/vlastroG/yocode/internal/mcpserver"
	"github.com/vlastroG/yocode/internal/testutil"
)

func TestClientListsAndCallsTools(t *testing.T) {
	ctx := context.Background()
	dir := testutil.GitRepo(t)

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(gittools.Repo{Dir: dir}).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	c, err := Connect(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	tools, err := c.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Function.Name] = true
		if tool.Type != "function" {
			t.Errorf("tool %s type = %q", tool.Function.Name, tool.Type)
		}
		if tool.Function.Name == "read_file" && !strings.Contains(string(tool.Function.Parameters), `"path"`) {
			t.Errorf("read_file schema = %s", tool.Function.Parameters)
		}
	}
	for _, want := range []string{"git_status", "git_log", "git_diff", "git_branch", "list_files", "read_file"} {
		if !names[want] {
			t.Errorf("missing tool %s (got %v)", want, names)
		}
	}

	out, err := c.Call(ctx, "read_file", map[string]any{"path": "README.md"})
	if err != nil || !strings.Contains(out, "hello") {
		t.Errorf("read_file = %q, %v", out, err)
	}
	if _, err := c.Call(ctx, "read_file", map[string]any{"path": "../x"}); err == nil {
		t.Error("expected error for path outside project")
	}
	out, err = c.Call(ctx, "git_status", nil)
	if err != nil || !strings.Contains(out, "main") {
		t.Errorf("git_status = %q, %v", out, err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/mcpclient/`
Expected: FAIL with `no required module provides package .../internal/mcpserver` or `undefined: Connect`

- [ ] **Step 4: Implement the server** `internal/mcpserver/server.go`

```go
// Package mcpserver exposes read-only project tools over MCP.
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/gittools"
)

type noArgs struct{}

type logArgs struct {
	N    int    `json:"n,omitempty" jsonschema:"number of commits to show (default 20)"`
	Path string `json:"path,omitempty" jsonschema:"only commits touching this file or directory"`
}

type diffArgs struct {
	Ref    string `json:"ref,omitempty" jsonschema:"commit, branch or range such as main..feature; empty means the working tree"`
	Staged bool   `json:"staged,omitempty" jsonschema:"show staged changes instead of unstaged ones"`
	Path   string `json:"path,omitempty" jsonschema:"limit the diff to this file or directory"`
}

type dirArgs struct {
	Path string `json:"path,omitempty" jsonschema:"directory relative to the project root; empty means the whole project"`
}

type fileArgs struct {
	Path string `json:"path" jsonschema:"file path relative to the project root"`
}

func New(repo gittools.Repo) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "yocode-git", Version: "0.1.0"}, nil)
	add(s, "git_status", "Show the working tree status (short format) and the current branch.",
		func(ctx context.Context, _ noArgs) (string, error) { return repo.Status(ctx) })
	add(s, "git_log", "Show recent commits (hash, date, author, subject).",
		func(ctx context.Context, a logArgs) (string, error) { return repo.Log(ctx, a.N, a.Path) })
	add(s, "git_diff", "Show a diff of the working tree, the staged changes, or against a ref.",
		func(ctx context.Context, a diffArgs) (string, error) { return repo.Diff(ctx, a.Ref, a.Staged, a.Path) })
	add(s, "git_branch", "List local and remote branches.",
		func(ctx context.Context, _ noArgs) (string, error) { return repo.Branches(ctx) })
	add(s, "list_files", "List files tracked by git, optionally under a directory.",
		func(ctx context.Context, a dirArgs) (string, error) { return repo.ListFiles(ctx, a.Path) })
	add(s, "read_file", "Read a text file from the project.",
		func(_ context.Context, a fileArgs) (string, error) { return repo.ReadFile(a.Path) })
	return s
}

// add registers a tool whose result is plain text; errors are reported to the model as tool errors.
func add[In any](s *mcp.Server, name, desc string, fn func(context.Context, In) (string, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			out, err := fn(ctx, in)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})
}
```

- [ ] **Step 5: Implement the client** `internal/mcpclient/client.go`

```go
// Package mcpclient connects to an MCP server and adapts its tools for Ollama.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/ollama"
)

type Client struct {
	session *mcp.ClientSession
}

// Start launches an MCP server binary and talks to it over stdio.
func Start(ctx context.Context, command string, args ...string) (*Client, error) {
	return Connect(ctx, &mcp.CommandTransport{Command: exec.Command(command, args...)})
}

func Connect(ctx context.Context, t mcp.Transport) (*Client, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "yocode", Version: "0.1.0"}, nil)
	s, err := c.Connect(ctx, t, nil)
	if err != nil {
		return nil, err
	}
	return &Client{session: s}, nil
}

func (c *Client) Close() error { return c.session.Close() }

// Tools returns the server's tools in Ollama's function-calling format.
func (c *Client) Tools(ctx context.Context) ([]ollama.Tool, error) {
	res, err := c.session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}
	tools := make([]ollama.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		params, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, err
		}
		tools = append(tools, ollama.Tool{
			Type:     "function",
			Function: ollama.ToolFunction{Name: t.Name, Description: t.Description, Parameters: params},
		})
	}
	return tools, nil
}

// Call runs a tool and returns its text output; tool-reported failures become errors.
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{} // the schema expects an object, not null
	}
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return "", errors.New(b.String())
	}
	return b.String(), nil
}
```

- [ ] **Step 6: Implement the server binary** `cmd/yocode-mcp/main.go`

```go
// Command yocode-mcp is a stdio MCP server with read-only git/file tools for one project.
package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/gittools"
	"github.com/vlastroG/yocode/internal/mcpserver"
)

func main() {
	dir := os.Getenv("PROJECT_DIR")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if dir == "" {
		dir = "/project"
	}
	s := mcpserver.New(gittools.Repo{Dir: dir})
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 7: Run the tests and build**

Run: `go test ./internal/mcpclient/ && go build ./...`
Expected: `ok`, and the build prints nothing

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/mcpserver internal/mcpclient cmd/yocode-mcp
git commit -m "feat(mcp): read-only git MCP server and Ollama-facing client"
```

---

### Task 8: Agent loop

**Files:**
- Create: `internal/agent/agent.go`
- Test: `internal/agent/agent_test.go`

- [ ] **Step 1: Write the failing test** `internal/agent/agent_test.go`

```go
package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/rag"
)

type fakeLLM struct {
	replies []ollama.Message
	calls   [][]ollama.Message
}

func (f *fakeLLM) Chat(_ context.Context, _ string, msgs []ollama.Message, _ []ollama.Tool) (ollama.Message, error) {
	f.calls = append(f.calls, append([]ollama.Message(nil), msgs...))
	if len(f.replies) == 0 {
		return ollama.Message{}, errors.New("no more replies")
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r, nil
}

type fakeTools struct{ called []string }

func (f *fakeTools) Tools(context.Context) ([]ollama.Tool, error) {
	return []ollama.Tool{{Type: "function", Function: ollama.ToolFunction{Name: "git_status"}}}, nil
}

func (f *fakeTools) Call(_ context.Context, name string, _ map[string]any) (string, error) {
	f.called = append(f.called, name)
	return "## main", nil
}

type fakeDocs struct{}

func (fakeDocs) Search(context.Context, string, int) ([]rag.Hit, error) {
	return []rag.Hit{{Source: "project/README.md", Text: "Source: project/README.md\n\nyocode is a docs assistant"}}, nil
}

func reply(s string) ollama.Message { return ollama.Message{Role: "assistant", Content: s} }

func toolCall(name string) ollama.Message {
	return ollama.Message{Role: "assistant", ToolCalls: []ollama.ToolCall{
		{Function: ollama.ToolCallFunction{Name: name, Arguments: map[string]any{}}},
	}}
}

func newAgent(llm *fakeLLM, tools *fakeTools) *Agent {
	return &Agent{LLM: llm, Model: "m", Tools: tools, Docs: fakeDocs{}}
}

func TestAnswerUsesDocs(t *testing.T) {
	llm := &fakeLLM{replies: []ollama.Message{reply("It is a docs assistant")}}
	ans, err := newAgent(llm, &fakeTools{}).Ask(context.Background(), "what is this?")
	if err != nil || ans != "It is a docs assistant" {
		t.Fatalf("ans=%q err=%v", ans, err)
	}
	msgs := llm.calls[0]
	if msgs[0].Role != "system" {
		t.Errorf("first message role = %q", msgs[0].Role)
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "yocode is a docs assistant") || !strings.Contains(last.Content, "Question: what is this?") {
		t.Errorf("user message = %q", last.Content)
	}
}

func TestToolCallLoop(t *testing.T) {
	llm := &fakeLLM{replies: []ollama.Message{toolCall("git_status"), reply("on main")}}
	tools := &fakeTools{}
	ans, err := newAgent(llm, tools).Ask(context.Background(), "which branch?")
	if err != nil || ans != "on main" {
		t.Fatalf("ans=%q err=%v", ans, err)
	}
	if !reflect.DeepEqual(tools.called, []string{"git_status"}) {
		t.Errorf("called = %v", tools.called)
	}
	second := llm.calls[1]
	toolMsg := second[len(second)-1]
	if toolMsg.Role != "tool" || toolMsg.Content != "## main" || toolMsg.ToolName != "git_status" {
		t.Errorf("tool message = %+v", toolMsg)
	}
}

func TestHistoryCarriesOver(t *testing.T) {
	llm := &fakeLLM{replies: []ollama.Message{reply("a1"), reply("a2")}}
	a := newAgent(llm, &fakeTools{})
	if _, err := a.Ask(context.Background(), "q1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Ask(context.Background(), "q2"); err != nil {
		t.Fatal(err)
	}
	second := llm.calls[1]
	if !reflect.DeepEqual(second[1], ollama.Message{Role: "user", Content: "q1"}) ||
		!reflect.DeepEqual(second[2], ollama.Message{Role: "assistant", Content: "a1"}) {
		t.Errorf("history = %+v", second[1:3])
	}
}

func TestStopsAfterMaxSteps(t *testing.T) {
	var replies []ollama.Message
	for range 10 {
		replies = append(replies, toolCall("git_status"))
	}
	_, err := newAgent(&fakeLLM{replies: replies}, &fakeTools{}).Ask(context.Background(), "loop")
	if err == nil {
		t.Error("expected an error after too many tool steps")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/agent/`
Expected: FAIL with `undefined: Agent`

- [ ] **Step 3: Implement** `internal/agent/agent.go`

```go
// Package agent answers questions using doc retrieval plus model tool calls.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/rag"
)

const systemPrompt = `You are yocode, a read-only assistant that helps the user understand a software project and its documentation.
- Base answers on the documentation excerpts and tool results. If they don't contain the answer, say so instead of guessing.
- Use the tools to inspect the repository (files, git status, history, diffs). You cannot modify anything.
- Mention the documentation source paths you relied on.
- Be concise.`

const (
	maxSteps   = 6  // model calls per question
	maxHistory = 10 // remembered messages (question/answer pairs) across questions
	docHits    = 5
)

type LLM interface {
	Chat(ctx context.Context, model string, msgs []ollama.Message, tools []ollama.Tool) (ollama.Message, error)
}

type Tools interface {
	Tools(ctx context.Context) ([]ollama.Tool, error)
	Call(ctx context.Context, name string, args map[string]any) (string, error)
}

type Retriever interface {
	Search(ctx context.Context, query string, n int) ([]rag.Hit, error)
}

type Agent struct {
	LLM   LLM
	Model string
	Tools Tools
	Docs  Retriever
	// OnToolCall, if set, is called before each tool call so the UI can show progress.
	OnToolCall func(name string, args map[string]any)

	toolDefs []ollama.Tool
	history  []ollama.Message
}

func (a *Agent) Ask(ctx context.Context, question string) (string, error) {
	if a.toolDefs == nil {
		defs, err := a.Tools.Tools(ctx)
		if err != nil {
			return "", fmt.Errorf("list tools: %w", err)
		}
		a.toolDefs = defs
	}
	hits, err := a.Docs.Search(ctx, question, docHits)
	if err != nil {
		return "", fmt.Errorf("search docs: %w", err)
	}

	msgs := []ollama.Message{{Role: "system", Content: systemPrompt}}
	msgs = append(msgs, a.history...)
	msgs = append(msgs, ollama.Message{Role: "user", Content: withDocs(question, hits)})

	for range maxSteps {
		resp, err := a.LLM.Chat(ctx, a.Model, msgs, a.toolDefs)
		if err != nil {
			return "", err
		}
		msgs = append(msgs, resp)
		if len(resp.ToolCalls) == 0 {
			a.remember(question, resp.Content)
			return resp.Content, nil
		}
		for _, tc := range resp.ToolCalls {
			if a.OnToolCall != nil {
				a.OnToolCall(tc.Function.Name, tc.Function.Arguments)
			}
			out, err := a.Tools.Call(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				out = "error: " + err.Error()
			}
			msgs = append(msgs, ollama.Message{Role: "tool", Content: out, ToolName: tc.Function.Name})
		}
	}
	return "", fmt.Errorf("no answer after %d tool steps", maxSteps)
}

// remember keeps the plain question (without doc excerpts) and the answer, so history stays small.
func (a *Agent) remember(question, answer string) {
	a.history = append(a.history,
		ollama.Message{Role: "user", Content: question},
		ollama.Message{Role: "assistant", Content: answer})
	if len(a.history) > maxHistory {
		a.history = a.history[len(a.history)-maxHistory:]
	}
}

func withDocs(question string, hits []rag.Hit) string {
	if len(hits) == 0 {
		return "No documentation excerpts matched this question.\n\nQuestion: " + question
	}
	var b strings.Builder
	b.WriteString("Documentation excerpts (may not all be relevant):\n\n")
	for i, h := range hits {
		fmt.Fprintf(&b, "[%d] %s\n\n", i+1, h.Text)
	}
	b.WriteString("Question: " + question)
	return b.String()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/agent/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/agent
git commit -m "feat(agent): RAG + tool-calling loop with short history"
```

---

### Task 9: TUI and `yocode` command

**Files:**
- Create: `internal/tui/tui.go`
- Create: `cmd/yocode/main.go`

This task is UI and wiring. All the logic underneath already has tests, so it's verified by building it here and by the Docker smoke test in Task 11.

- [ ] **Step 1: Add the dependencies**

```bash
go get github.com/charmbracelet/bubbletea@v1.3.4 github.com/charmbracelet/bubbles@v0.20.0 github.com/charmbracelet/lipgloss@v1.0.0
```

- [ ] **Step 2: Implement** `internal/tui/tui.go`

```go
// Package tui is a minimal chat interface: scrollback, a spinner while thinking, one input line.
package tui

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vlastroG/yocode/internal/agent"
)

var (
	userStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	botStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

type answerMsg struct {
	text string
	err  error
}

type toolMsg string

type model struct {
	agent *agent.Agent
	input textinput.Model
	view  viewport.Model
	spin  spinner.Model
	lines []string
	busy  bool
	width int
}

// Run starts the chat UI. intro is shown as the first line (index/model info).
func Run(a *agent.Agent, intro string) error {
	in := textinput.New()
	in.Placeholder = "Ask about the docs, files or git history (/exit to quit)"
	in.Prompt = "> "
	in.CharLimit = 2000
	in.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := model{agent: a, input: in, view: viewport.New(80, 20), spin: sp, lines: []string{dimStyle.Render(intro)}}
	m.refresh()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	a.OnToolCall = func(name string, args map[string]any) { p.Send(toolMsg(formatCall(name, args))) }
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.view.Width = msg.Width
		m.view.Height = max(msg.Height-3, 1)
		m.input.Width = max(msg.Width-4, 10)
		m.refresh()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "pgup", "pgdown":
			var cmd tea.Cmd
			m.view, cmd = m.view.Update(msg)
			return m, cmd
		case "enter":
			q := strings.TrimSpace(m.input.Value())
			if q == "" || m.busy {
				return m, nil
			}
			if q == "/exit" || q == "/quit" {
				return m, tea.Quit
			}
			m.input.Reset()
			m.busy = true
			m.add(userStyle.Render("you › ") + q)
			return m, tea.Batch(m.spin.Tick, ask(m.agent, q))
		}
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	case toolMsg:
		m.add(dimStyle.Render("  ↳ " + string(msg)))
		return m, nil
	case answerMsg:
		m.busy = false
		if msg.err != nil {
			m.add(errStyle.Render("error: " + msg.err.Error()))
		} else {
			m.add(botStyle.Render("yocode › ") + msg.text)
		}
		return m, nil
	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	status := dimStyle.Render("enter: ask · pgup/pgdn or mouse: scroll · esc: quit")
	if m.busy {
		status = m.spin.View() + " thinking..."
	}
	return m.view.View() + "\n" + status + "\n" + m.input.View()
}

func (m *model) add(line string) {
	m.lines = append(m.lines, line)
	m.refresh()
}

func (m *model) refresh() {
	m.view.SetContent(lipgloss.NewStyle().Width(m.width).Render(strings.Join(m.lines, "\n\n")))
	m.view.GotoBottom()
}

func ask(a *agent.Agent, q string) tea.Cmd {
	return func() tea.Msg {
		text, err := a.Ask(context.Background(), q)
		return answerMsg{text: text, err: err}
	}
}

func formatCall(name string, args map[string]any) string {
	b, _ := json.Marshal(args)
	if s := string(b); s != "{}" && s != "null" {
		return name + " " + s
	}
	return name
}
```

- [ ] **Step 3: Implement** `cmd/yocode/main.go`

```go
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
  EMBED_MODEL    Ollama embedding model (default nomic-embed-text)
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
```

- [ ] **Step 4: Build, vet and run all tests**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no build or vet output, and every package `ok`

- [ ] **Step 5: Check help**

Run: `go run ./cmd/yocode help`
Expected: the usage text is printed and the exit code is 0

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/tui cmd/yocode
git commit -m "feat: yocode CLI with chat TUI and one-shot ask"
```

---

### Task 10: Docker and Compose

**Files:**
- Create: `Dockerfile`, `compose.yaml`, `.dockerignore`, `.env.example`, `.nodocs/.gitkeep`

- [ ] **Step 1: Create** `Dockerfile`

```dockerfile
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/yocode ./cmd/yocode \
 && CGO_ENABLED=0 go build -o /out/yocode-mcp ./cmd/yocode-mcp

FROM alpine:3.20
# The project is bind-mounted from Windows: trust any owner and normalize line endings and file modes,
# so `git status` doesn't report every file as modified.
RUN apk add --no-cache git ca-certificates \
 && git config --system --add safe.directory '*' \
 && git config --system core.autocrlf true \
 && git config --system core.fileMode false
COPY --from=build /out/ /usr/local/bin/
ENV PROJECT_DIR=/project \
    DOCS_DIR=/docs \
    DATA_DIR=/data \
    OLLAMA_URL=http://ollama:11434 \
    YOCODE_MCP=yocode-mcp \
    TERM=xterm-256color
WORKDIR /project
ENTRYPOINT ["yocode"]
```

- [ ] **Step 2: Create** `compose.yaml`

```yaml
services:
  ollama:
    image: ollama/ollama:0.12.6
    volumes:
      - ollama-models:/root/.ollama
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
    healthcheck:
      test: ["CMD", "ollama", "list"]
      interval: 5s
      timeout: 5s
      retries: 20

  yocode:
    build: .
    depends_on:
      ollama:
        condition: service_healthy
    stdin_open: true
    tty: true
    environment:
      PROJECT_HOST_PATH: ${PROJECT_PATH:?set PROJECT_PATH in .env}
      DOCS_HOST_PATH: ${DOCS_PATH:-}
      CHAT_MODEL: ${CHAT_MODEL:-qwen2.5:7b}
      EMBED_MODEL: ${EMBED_MODEL:-nomic-embed-text}
      REINDEX: ${REINDEX:-false}
    volumes:
      - ${PROJECT_PATH:?set PROJECT_PATH in .env}:/project:ro
      - ${DOCS_PATH:-./.nodocs}:/docs:ro
      - yocode-data:/data

volumes:
  ollama-models:
  yocode-data:
```

If the `ollama/ollama:0.12.6` tag doesn't exist, use the latest `0.x` tag listed on Docker Hub. The version only needs to support `qwen2.5` and tool calling (≥ 0.4).

- [ ] **Step 3: Create** `.dockerignore`, `.env.example` and `.nodocs/.gitkeep`

`.dockerignore`:

```
.git
.env
docs
.nodocs
```

`.env.example`:

```
# Host path of the project to work with (forward slashes are fine on Windows).
PROJECT_PATH=C:/Users/you/source/repos/myproject

# Optional: separate docs project (e.g. a Hugo site) for the same product.
# DOCS_PATH=C:/Users/you/source/repos/myproject-docs

# CHAT_MODEL=qwen2.5:7b
# EMBED_MODEL=nomic-embed-text

# Set to true once to rebuild the docs index (e.g. after the docs changed).
# REINDEX=false
```

```bash
mkdir -p .nodocs && touch .nodocs/.gitkeep
```

- [ ] **Step 4: Validate the compose file**

Run: `PROJECT_PATH=. docker compose config --quiet`
Expected: no output and exit code 0. Start Docker Desktop first.

- [ ] **Step 5: Build the image**

Run: `docker compose build yocode`
Expected: ends with `yocode  Built` (or a similar success line)

- [ ] **Step 6: Commit**

```bash
git add Dockerfile compose.yaml .dockerignore .env.example .nodocs/.gitkeep
git commit -m "build: docker compose setup with GPU ollama"
```

---

### Task 11: End-to-end smoke test and docs

**Files:**
- Modify: `README.md`, `CLAUDE.md`

- [ ] **Step 1: Point yocode at itself**

Create `.env` with `PROJECT_PATH=C:/Users/vlast/source/repos/yocode`.

- [ ] **Step 2: First run (pulls models, builds the index)**

Run (PowerShell or Windows Terminal; Git Bash's mintty needs `winpty` for TTY apps):

```bash
docker compose run --rm yocode ask "What is yocode and how do I run it?"
```

Expected on stderr: `pulling model qwen2.5:7b ...` (≈5 GB, first run only), `preparing docs index...`, `docs index docs-xxxxxxxxxxxxxxxx: built (N chunks)` with N > 0. Expected on stdout: an answer that mentions Docker Compose or `PROJECT_PATH` and cites `project/README.md` or the plan doc.

- [ ] **Step 3: Second run reuses the index**

```bash
docker compose run --rm yocode ask "Which models does yocode use by default?"
```

Expected: no pull lines, and `docs index ...: reused`.

- [ ] **Step 4: Forced reindex**

```bash
docker compose run --rm -e REINDEX=true yocode ask "Which models does yocode use by default?"
```

Expected: `docs index ...: built`.

- [ ] **Step 5: Tools through MCP**

```bash
docker compose run --rm yocode ask "Show the git status and the last 3 commits."
```

Expected: stderr lines such as `tool: git_status map[]` and `tool: git_log map[n:3]`, and an answer listing the real commits. If the status shows every file as modified, the CRLF/fileMode git config in the Dockerfile isn't taking effect: check `docker compose run --rm --entrypoint git yocode -C /project config --list --show-origin`.

- [ ] **Step 6: GPU check**

While a question is running, in another terminal:

```bash
docker compose exec ollama ollama ps
```

Expected: `qwen2.5:7b` with `100% GPU` in the PROCESSOR column.

- [ ] **Step 7: Interactive TUI**

```bash
docker compose run --rm yocode
```

Expected: a full-screen chat. Ask a question; a spinner shows, tool calls appear as `↳ git_status`, and the answer is printed. `esc` quits.

- [ ] **Step 8: Update** `README.md`

```markdown
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

The first run downloads the models (about 5 GB) and builds the docs index. Later runs reuse both. Set `REINDEX=true` in `.env` (or pass `-e REINDEX=true`) to rebuild the index after the docs change. Stop Ollama with `docker compose down`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PROJECT_PATH` | required | Project to work with, mounted read-only |
| `DOCS_PATH` | none | Separate docs project |
| `CHAT_MODEL` | `qwen2.5:7b` | Ollama chat model (must support tool calling) |
| `EMBED_MODEL` | `nomic-embed-text` | Ollama embedding model |
| `REINDEX` | `false` | Rebuild the docs index on start |

## Tools available to the assistant

`git_status`, `git_log`, `git_diff`, `git_branch`, `list_files`, `read_file`, all read-only.
```

- [ ] **Step 9: Update** `CLAUDE.md`

Replace the "Project status", "Commands" and "Architecture" sections with:

```markdown
## Commands

- Unit tests (no Docker or Ollama needed): `go test ./...`
- Single test: `go test ./internal/agent -run '^TestToolCallLoop$'`
- Build/vet: `go build ./... && go vet ./...`
- Run everything: set `PROJECT_PATH` in `.env`, then `docker compose run --rm yocode` (TUI) or `docker compose run --rm yocode ask "..."`
- Rebuild the image after Go changes: `docker compose build yocode`

## Architecture

yocode is a read-only POC: a CLI in the `yocode` container talks to Ollama in the `ollama` container (GPU).

- `cmd/yocode/main.go` `setup()` is the wiring point: it pulls missing models, builds or reuses the docs index, and spawns `yocode-mcp` over stdio.
- Docs RAG: `internal/docs` (discover + heading-aware chunking) → `internal/rag` (chromem-go, persisted under `/data/vectors`). The collection name is `config.IndexKey` = hash(project host path, docs host path, embedding model), so a known project reuses its embeddings; `REINDEX=true` rebuilds.
- Tools: `internal/gittools` (git and file reads, path-escape and option-injection guards) is exposed by `internal/mcpserver` (go-sdk MCP) through the `cmd/yocode-mcp` binary. `internal/mcpclient` converts MCP tools to Ollama's function-calling format.
- `internal/agent`: each question gets the top 5 doc chunks injected, then a loop of up to 6 `/api/chat` calls that execute tool calls. History keeps only plain question/answer pairs.
- `internal/tui`: Bubble Tea; `agent.OnToolCall` streams tool calls into the UI.

The project is mounted `:ro` and git runs with `GIT_OPTIONAL_LOCKS=0`. The Dockerfile sets system git config (`safe.directory`, `autocrlf`, `fileMode`) so Windows checkouts don't show up as modified.
```

- [ ] **Step 10: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: usage and architecture for the POC"
```

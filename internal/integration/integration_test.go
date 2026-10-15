//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vlastroG/yocode/internal/app"
	"github.com/vlastroG/yocode/internal/commands"
	"github.com/vlastroG/yocode/internal/config"
	"github.com/vlastroG/yocode/internal/gittools"
	"github.com/vlastroG/yocode/internal/mcpclient"
	"github.com/vlastroG/yocode/internal/mcpserver"
)

const (
	usagesGoal    = "find all places where greet.Hello is used and list them in docs/usages.md"
	changelogGoal = "write CHANGELOG.md from the git history"
)

// docsDir is an empty DOCS_DIR: the tests have no separate docs project.
var docsDir string

func TestMain(m *testing.M) {
	if os.Getenv("OLLAMA_URL") == "" {
		fmt.Println("OLLAMA_URL is not set: skipping the integration tests")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "yocode-nodocs")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	docsDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// recorder logs tool calls and counts the failed and repeated ones.
type recorder struct {
	t        *testing.T
	mu       sync.Mutex
	seen     map[string]bool
	calls    int
	failed   int
	repeated int
}

func (r *recorder) onToolCall(name string, args map[string]any) {
	key, _ := json.Marshal(args)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.seen[name+string(key)] {
		r.repeated++
		r.t.Logf("tool (repeated): %s %s", name, key)
		return
	}
	r.seen[name+string(key)] = true
	r.t.Logf("tool: %s %s", name, key)
}

// middleware logs the tool calls that fail on the server.
func (r *recorder) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		call, ok := req.(*mcp.CallToolRequest)
		if !ok {
			return res, err
		}
		if out, isResult := res.(*mcp.CallToolResult); err != nil || (isResult && out.IsError) {
			msg := fmt.Sprint(err)
			if err == nil && len(out.Content) > 0 {
				if tc, ok := out.Content[0].(*mcp.TextContent); ok {
					msg = tc.Text
				}
			}
			r.mu.Lock()
			r.failed++
			r.mu.Unlock()
			r.t.Logf("tool failed: %s: %s", call.Params.Name, cut(msg, 300))
		}
		return res, err
	}
}

// newApp wires yocode for repo, with its tools served in memory.
func newApp(t *testing.T, repo, data string) (*app.App, *recorder) {
	t.Helper()
	env := map[string]string{"PROJECT_DIR": repo, "DATA_DIR": data, "DOCS_DIR": docsDir}
	cfg := config.FromEnv(func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return os.Getenv(k)
	})
	rec := &recorder{t: t, seen: map[string]bool{}}
	a, err := app.Setup(context.Background(), cfg, func(ctx context.Context) (*mcpclient.Client, error) {
		s := mcpserver.New(gittools.Repo{Dir: repo})
		s.AddReceivingMiddleware(rec.middleware)
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(ctx, st, nil); err != nil {
			return nil, err
		}
		return mcpclient.Connect(ctx, ct)
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(a.Close)
	a.Agent.OnToolCall = rec.onToolCall
	a.Task.Planner.OnToolCall = rec.onToolCall
	a.Task.Executor.OnToolCall = rec.onToolCall
	a.Task.Executor.OnProgress = func(s string) { t.Log(s) }
	t.Logf("model %s, task mode single=%v", cfg.ChatModel, cfg.TaskSingle)
	return a, rec
}

// run runs a CLI command (args[0]) on repo like `yocode <args>` and logs its tool calls and result.
func run(t *testing.T, repo, data string, args ...string) (string, error) {
	t.Helper()
	a, rec := newApp(t, repo, data)
	c, ok := commands.New(a.Deps(nil)).CLI().Lookup(args[0])
	if !ok {
		t.Fatalf("unknown command %q", args[0])
	}
	if err := c.Check(args[1:]); err != nil {
		t.Fatalf("check %v: %v", args, err)
	}
	start := time.Now()
	out, err := c.Run(context.Background(), args[1:])
	t.Logf("yocode %q: %s, %d tool calls (%d failed, %d repeated), error: %v\n%s",
		args, time.Since(start).Round(time.Second), rec.calls, rec.failed, rec.repeated, err, out)
	return out, err
}

// newScenario returns a data dir and a fresh fixture repo, and fails the test if anything
// appears next to them.
func newScenario(t *testing.T) (repo, data string) {
	t.Helper()
	data = t.TempDir()
	repo = newRepo(t)
	expectNoStrayFiles(t, filepath.Dir(repo))
	return repo, data
}

// expectNoStrayFiles fails the test if dir or the docs dir gets new entries by the end of the test.
func expectNoStrayFiles(t *testing.T, dir string) {
	t.Helper()
	before := entries(t, dir)
	t.Cleanup(func() {
		if after := entries(t, dir); !slices.Equal(before, after) {
			t.Errorf("entries next to the repo changed: %v -> %v", before, after)
		}
		if docs := entries(t, docsDir); len(docs) > 0 {
			t.Errorf("files written to the docs dir: %v", docs)
		}
	})
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func readFile(t *testing.T, repo, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func expectChanged(t *testing.T, repo string, want ...string) {
	t.Helper()
	if got := changedFiles(t, repo); !slices.Equal(got, want) {
		t.Errorf("changed files = %v, want %v\n%s", got, want, gitOut(t, repo, "status", "--short"))
	}
}

func TestAskFindsCode(t *testing.T) {
	repo, data := newScenario(t)
	out, err := run(t, repo, data, "ask", "which function says goodbye and where is it called?")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Goodbye", "greet.go", "main.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("answer lacks %q", want)
		}
	}
	expectChanged(t, repo)
}

func TestTaskFindUsages(t *testing.T) {
	repo, data := newScenario(t)
	if _, err := run(t, repo, data, "task", usagesGoal, "--yes"); err != nil {
		t.Fatal(err)
	}
	checkUsages(t, repo)
}

// checkUsages checks the docs/usages.md the usages goal asks for.
func checkUsages(t *testing.T, repo string) {
	t.Helper()
	expectChanged(t, repo, "docs/usages.md")
	if t.Failed() {
		return
	}
	doc := readFile(t, repo, "docs/usages.md")
	t.Logf("docs/usages.md:\n%s", doc)
	if !strings.Contains(doc, "cmd/app/main.go") {
		t.Error("docs/usages.md doesn't mention cmd/app/main.go")
	}
	// lines of the real call sites, e.g. "cmd/app/main.go:10:..."
	var lines []string
	for _, l := range strings.Split(gitOut(t, repo, "grep", "-n", "-F", "greet.Hello", "--", "cmd/app/main.go"), "\n") {
		if parts := strings.SplitN(l, ":", 3); len(parts) == 3 {
			lines = append(lines, parts[1])
		}
	}
	for _, n := range lines {
		if regexp.MustCompile(`\b` + n + `\b`).MatchString(doc) {
			return
		}
	}
	t.Errorf("docs/usages.md has none of the call lines %v", lines)
}

func TestTaskGenerateChangelog(t *testing.T) {
	repo, data := newScenario(t)
	if _, err := run(t, repo, data, "task", changelogGoal, "--yes"); err != nil {
		t.Fatal(err)
	}
	expectChanged(t, repo, "CHANGELOG.md")
	if t.Failed() {
		return
	}
	checkChangelog(t, readFile(t, repo, "CHANGELOG.md"))
}

func checkChangelog(t *testing.T, log string) {
	t.Helper()
	t.Logf("CHANGELOG.md:\n%s", log)
	found := 0
	for _, s := range fixtureCommits {
		if strings.Contains(strings.ToLower(log), s) {
			found++
		}
	}
	if found < 2 {
		t.Errorf("CHANGELOG.md contains %d of the commit subjects %q, want at least 2", found, fixtureCommits)
	}
}

func TestTaskAddDocs(t *testing.T) {
	repo, data := newScenario(t)
	orig := readFile(t, repo, "greet/greet.go")
	if _, err := run(t, repo, data, "task", "add doc comments to all exported functions and types in the greet package", "--yes"); err != nil {
		t.Fatal(err)
	}
	expectChanged(t, repo, "greet/greet.go")
	src := readFile(t, repo, "greet/greet.go")
	t.Logf("greet/greet.go:\n%s", src)

	f, err := parser.ParseFile(token.NewFileSet(), "greet.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() && d.Doc == nil {
				t.Errorf("func %s has no doc comment", d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				if ts, ok := s.(*ast.TypeSpec); ok && ts.Name.IsExported() && d.Doc == nil && ts.Doc == nil {
					t.Errorf("type %s has no doc comment", ts.Name.Name)
				}
			}
		}
	}
	if a, b := withoutComments(t, orig), withoutComments(t, src); a != b {
		t.Errorf("code other than comments changed:\n--- before\n%s\n--- after\n%s", a, b)
	}
	if out, err := exec.Command("go", "-C", repo, "vet", "./...").CombinedOutput(); err != nil {
		t.Errorf("go vet: %v\n%s", err, out)
	}
}

// withoutComments prints Go source with all comments dropped.
func withoutComments(t *testing.T, src string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, f); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestTaskUpdateDocs(t *testing.T) {
	repo, data := newScenario(t)
	code := readFile(t, repo, "greet/greet.go") + `
func Greet(name, lang string) string {
	switch lang {
	case "es":
		return "Hola, " + clean(name) + "!"
	case "de":
		return "Hallo, " + clean(name) + "!"
	}
	return Hello(name)
}
`
	if err := os.WriteFile(filepath.Join(repo, "greet/greet.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, repo, "commit", "-am", "add Greet with a language")

	if _, err := run(t, repo, data, "task", "update docs/usage.md for the latest code changes", "--yes"); err != nil {
		t.Fatal(err)
	}
	expectChanged(t, repo, "docs/usage.md")
	doc := readFile(t, repo, "docs/usage.md")
	t.Logf("docs/usage.md:\n%s", doc)
	if !strings.Contains(doc, "Greet(") {
		t.Error("docs/usage.md doesn't mention Greet(")
	}
}

func TestTaskBranchAndCommit(t *testing.T) {
	repo, data := newScenario(t)
	mainRev := gitOut(t, repo, "rev-parse", "main")
	if _, err := run(t, repo, data, "task", "create branch docs/changelog, write CHANGELOG.md from the git history and commit it", "--yes"); err != nil {
		t.Fatal(err)
	}
	if b := gitOut(t, repo, "branch", "--show-current"); b != "docs/changelog" {
		t.Errorf("current branch = %q, want docs/changelog", b)
	}
	if files := gitOut(t, repo, "show", "--name-only", "--format=", "HEAD"); !slices.Contains(strings.Fields(files), "CHANGELOG.md") {
		t.Errorf("HEAD commit files = %q, want CHANGELOG.md", files)
	}
	expectChanged(t, repo)
	if now := gitOut(t, repo, "rev-parse", "main"); now != mainRev {
		t.Errorf("main moved from %s to %s", mainRev, now)
	}
	t.Logf("git log --all:\n%s", gitOut(t, repo, "log", "--all", "--oneline", "--decorate"))
}

var planID = regexp.MustCompile(`task --plan ([a-z0-9-]+)`)

func TestTaskReplayIsReproducible(t *testing.T) {
	data := t.TempDir()
	first, second := newRepo(t), newRepo(t)
	expectNoStrayFiles(t, filepath.Dir(first))

	out, err := run(t, first, data, "task", usagesGoal, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	m := planID.FindStringSubmatch(out)
	if m == nil {
		t.Fatal("the result has no plan id")
	}
	if _, err := run(t, second, data, "task", "--plan", m[1]); err != nil {
		t.Fatal(err)
	}
	a, b := changedFiles(t, first), changedFiles(t, second)
	if !slices.Equal(a, b) {
		t.Errorf("changed files differ: first run %v, replay %v", a, b)
	}
	for _, f := range a {
		if slices.Contains(b, f) && readFile(t, first, f) != readFile(t, second, f) {
			t.Logf("%s differs:\n--- first run\n%s\n--- replay\n%s", f, readFile(t, first, f), readFile(t, second, f))
		}
	}
}

func TestDiffShowsTaskChanges(t *testing.T) {
	repo, data := newScenario(t)
	if _, err := run(t, repo, data, "task", usagesGoal, "--yes"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changedFiles(t, repo), "docs/usages.md") {
		t.Fatalf("the task didn't create docs/usages.md: %v", changedFiles(t, repo))
	}
	out, err := run(t, repo, data, "diff")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"diff --git a/docs/usages.md", "new file"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff lacks %q", want)
		}
	}
}

func TestPlanRevision(t *testing.T) {
	repo, data := newScenario(t)
	a, rec := newApp(t, repo, data)
	ctx := context.Background()
	plan, err := a.Task.Start(ctx, changelogGoal)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("plan:\n%s", plan.Markdown())
	revised, err := a.Task.Revise(ctx, "also add a short summary section at the top of README.md")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("revised plan (%d tool calls, %d failed):\n%s", rec.calls, rec.failed, revised.Markdown())
	a.Task.Cancel()
	mentions := false
	for _, s := range revised.Steps {
		if strings.Contains(strings.ToLower(s.Title+" "+s.Instructions), "readme") {
			mentions = true
		}
	}
	if !mentions {
		t.Error("no step of the revised plan mentions README")
	}
	expectChanged(t, repo)
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

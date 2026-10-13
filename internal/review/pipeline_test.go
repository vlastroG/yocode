package review

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/rag"
)

type fakeTools struct {
	changed string
	calls   []string
}

func (f *fakeTools) Call(_ context.Context, name string, args map[string]any) (string, error) {
	f.calls = append(f.calls, name+" "+mustJSON(args))
	switch name {
	case "git_changed_files":
		return f.changed, nil
	case "git_log":
		return "abc123 2026-10-13 dev: add feature", nil
	case "git_diff":
		return "diff --git a/internal/a.go b/internal/a.go\n--- a/internal/a.go\n+++ b/internal/a.go\n" +
			"@@ -1,2 +1,3 @@\n package a\n+var x = os.Getenv(\"SECRET\")\n func A() {}\n", nil
	}
	return "", errors.New("unexpected tool " + name)
}

type reply struct {
	json string
	err  error
}

type fakeLLM struct {
	replies []reply
	calls   [][]ollama.Message
}

func (f *fakeLLM) ChatJSON(_ context.Context, _ string, msgs []ollama.Message, schema json.RawMessage, out any) error {
	if !json.Valid(schema) {
		return errors.New("invalid schema")
	}
	f.calls = append(f.calls, msgs)
	r := f.replies[0]
	f.replies = f.replies[1:]
	if r.err != nil {
		return r.err
	}
	return json.Unmarshal([]byte(r.json), out)
}

type fakeDocs struct{}

func (fakeDocs) Search(context.Context, string, int) ([]rag.Hit, error) {
	return []rag.Hit{{Source: "docs/config.md", Text: "Source: docs/config.md\n\nSetting FOO controls the secret."}}, nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func newPipeline(tools *fakeTools, llm *fakeLLM, progress *[]string) *Pipeline {
	return &Pipeline{LLM: llm, Model: "m", Tools: tools, Docs: fakeDocs{}, Language: "Russian",
		OnProgress: func(s string) { *progress = append(*progress, s) }}
}

func TestRunReviewsFilesAndSummarizes(t *testing.T) {
	tools := &fakeTools{changed: "M\tinternal/a.go\nA\tgo.sum\nD\told.go\n"}
	llm := &fakeLLM{replies: []reply{
		{json: `{"findings":[{"line":2,"severity":"medium","category":"security","message":"reads secret"}]}`},
		{json: `{"summary":"Adds x.","findings":[{"severity":"low","category":"docs","message":"update docs/config.md"}]}`},
	}}
	var progress []string

	rep, err := newPipeline(tools, llm, &progress).Run(context.Background(), Args{Base: "main", Head: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Range != "main...feature" || rep.Summary != "Adds x." {
		t.Errorf("range=%q summary=%q", rep.Range, rep.Summary)
	}
	if !reflect.DeepEqual(rep.Reviewed, []string{"internal/a.go"}) {
		t.Errorf("reviewed = %v", rep.Reviewed)
	}
	if !reflect.DeepEqual(rep.Skipped, []Skipped{{"go.sum", "lockfile"}, {"old.go", "deleted"}}) {
		t.Errorf("skipped = %v", rep.Skipped)
	}
	want := []Finding{
		{File: "internal/a.go", Line: 2, Severity: "medium", Category: "security", Message: "reads secret"},
		{Severity: "low", Category: "docs", Message: "update docs/config.md"},
	}
	if !reflect.DeepEqual(rep.Findings, want) {
		t.Errorf("findings = %+v", rep.Findings)
	}

	wantCalls := []string{
		`git_changed_files {"ref":"main...feature"}`,
		`git_log {"n":50,"ref":"main..feature"}`,
		`git_diff {"path":"internal/a.go","ref":"main...feature"}`,
	}
	if !reflect.DeepEqual(tools.calls, wantCalls) {
		t.Errorf("tool calls = %v", tools.calls)
	}

	fileMsgs := llm.calls[0]
	if !strings.Contains(fileMsgs[0].Content, "Write all text in Russian.") {
		t.Errorf("system prompt = %q", fileMsgs[0].Content)
	}
	user := fileMsgs[1].Content
	for _, part := range []string{"    2 +var x", "Setting FOO", "abc123", "File: internal/a.go (modified)"} {
		if !strings.Contains(user, part) {
			t.Errorf("file request lacks %q:\n%s", part, user)
		}
	}
	if summary := llm.calls[1][1].Content; !strings.Contains(summary, "[medium/security] internal/a.go:2 — reads secret") {
		t.Errorf("summary request = %q", summary)
	}
	if !reflect.DeepEqual(progress, []string{"reviewing internal/a.go (1/1)", "summarizing"}) {
		t.Errorf("progress = %v", progress)
	}
}

func TestRunContinuesWhenAFileFails(t *testing.T) {
	tools := &fakeTools{changed: "M\tinternal/a.go\n"}
	llm := &fakeLLM{replies: []reply{
		{err: errors.New("ollama down")},
		{json: `{"summary":"Nothing reviewed.","findings":[]}`},
	}}
	var progress []string

	rep, err := newPipeline(tools, llm, &progress).Run(context.Background(), Args{Base: "main", Head: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Reviewed) != 0 || len(rep.Skipped) != 1 || !strings.HasPrefix(rep.Skipped[0].Reason, "review failed: ollama down") {
		t.Errorf("reviewed=%v skipped=%v", rep.Reviewed, rep.Skipped)
	}
}

func TestRunWithoutChanges(t *testing.T) {
	var progress []string
	_, err := newPipeline(&fakeTools{changed: "(no output)"}, &fakeLLM{}, &progress).Run(context.Background(), Args{Base: "main", Head: "main"})
	if err == nil || !strings.Contains(err.Error(), "no changes") {
		t.Errorf("err = %v", err)
	}
}

func TestRunStopsBeforeReviewingTooManyFiles(t *testing.T) {
	tools := &fakeTools{changed: "M\ta.go\nM\tb.go\nM\tc.go\nM\tgo.sum\n"} // 3 reviewable files
	llm := &fakeLLM{}
	var progress []string
	p := newPipeline(tools, llm, &progress)
	p.MaxFiles = 2

	_, err := p.Run(context.Background(), Args{Base: "main", Head: "feature"})
	var tooMany *TooManyFilesError
	if !errors.As(err, &tooMany) || tooMany.Files != 3 || tooMany.Limit != 2 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "review main feature --max-files 3") {
		t.Errorf("message lacks the command to raise the limit: %v", err)
	}
	if len(llm.calls) != 0 || len(tools.calls) != 1 || len(progress) != 0 {
		t.Errorf("must stop before any review work: llm=%d tools=%v progress=%v", len(llm.calls), tools.calls, progress)
	}

	// A per-run --max-files overrides the pipeline's limit.
	p.LLM = &fakeLLM{replies: []reply{
		{json: `{"findings":[]}`}, {json: `{"findings":[]}`}, {json: `{"findings":[]}`},
		{json: `{"summary":"ok","findings":[]}`},
	}}
	rep, err := p.Run(context.Background(), Args{Base: "main", Head: "feature", MaxFiles: 3})
	if err != nil || len(rep.Reviewed) != 3 {
		t.Errorf("with --max-files 3: report=%+v err=%v", rep, err)
	}
}

func TestParseArgs(t *testing.T) {
	cases := []struct {
		in   []string
		want Args
	}{
		{[]string{"main"}, Args{Base: "main", Head: "HEAD"}},
		{[]string{"main", "feature"}, Args{Base: "main", Head: "feature"}},
		{[]string{"main", "feature", "--max-files", "80"}, Args{Base: "main", Head: "feature", MaxFiles: 80}},
		{[]string{"--max-files=80", "main"}, Args{Base: "main", Head: "HEAD", MaxFiles: 80}},
	}
	for _, c := range cases {
		if got, err := ParseArgs(c.in); err != nil || got != c.want {
			t.Errorf("ParseArgs(%v) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	bad := [][]string{nil, {"a", "b", "c"}, {"main", "--max-files"}, {"main", "--max-files", "0"}, {"main", "--max-files=x"}}
	for _, args := range bad {
		if _, err := ParseArgs(args); err == nil {
			t.Errorf("ParseArgs(%v) accepted", args)
		}
	}
}

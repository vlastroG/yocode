package task

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

// fakeLLM answers Chat from a script and ChatJSON from a queue of JSON documents.
type fakeLLM struct {
	chat      func(call int, msgs []ollama.Message, tools []ollama.Tool) (ollama.Message, error)
	jsons     []string
	chatCalls [][]ollama.Message
	toolDefs  [][]ollama.Tool
	jsonCalls [][]ollama.Message
}

func (f *fakeLLM) Chat(_ context.Context, _ string, msgs []ollama.Message, tools []ollama.Tool) (ollama.Message, error) {
	f.chatCalls = append(f.chatCalls, append([]ollama.Message(nil), msgs...))
	f.toolDefs = append(f.toolDefs, tools)
	if f.chat == nil {
		return reply("ok"), nil
	}
	return f.chat(len(f.chatCalls)-1, msgs, tools)
}

func (f *fakeLLM) ChatJSON(_ context.Context, _ string, msgs []ollama.Message, _ json.RawMessage, out any) error {
	f.jsonCalls = append(f.jsonCalls, append([]ollama.Message(nil), msgs...))
	if len(f.jsons) == 0 {
		return errors.New("no more json replies")
	}
	s := f.jsons[0]
	f.jsons = f.jsons[1:]
	return json.Unmarshal([]byte(s), out)
}

type fakeTools struct{ calls []string }

func (f *fakeTools) Tools(context.Context) ([]ollama.Tool, error) {
	return []ollama.Tool{{Type: "function", Function: ollama.ToolFunction{Name: "read_file"}}}, nil
}

func (f *fakeTools) Call(_ context.Context, name string, _ map[string]any) (string, error) {
	f.calls = append(f.calls, name)
	return "TOOL-OUTPUT " + name, nil
}

type fakeDocs struct{}

func (fakeDocs) Search(context.Context, string, int) ([]rag.Hit, error) {
	return []rag.Hit{{Source: "docs/a.md", Text: "Source: docs/a.md\n\nX is a thing"}}, nil
}

func reply(s string) ollama.Message { return ollama.Message{Role: "assistant", Content: s} }

func toolCall(name string) ollama.Message {
	return ollama.Message{Role: "assistant", ToolCalls: []ollama.ToolCall{
		{Function: ollama.ToolCallFunction{Name: name, Arguments: map[string]any{}}},
	}}
}

func joined(msgs []ollama.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content + "\n")
	}
	return b.String()
}

const twoSteps = `{"steps":[{"title":"Find X","instructions":"grep X and list it in X.md","access":"edit"},` +
	`{"title":"Edit README","instructions":"document X in README.md","access":"edit"}]}`

func newPlanner(llm *fakeLLM) *Planner {
	return &Planner{LLM: llm, Model: "m", Tools: &fakeTools{}, Docs: fakeDocs{}}
}

func TestDraft(t *testing.T) {
	llm := &fakeLLM{
		chat: func(int, []ollama.Message, []ollama.Tool) (ollama.Message, error) {
			return reply("notes: README.md documents X"), nil
		},
		jsons: []string{twoSteps},
	}
	plan, notes, err := newPlanner(llm).Draft(context.Background(), "document X")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Goal != "document X" || len(plan.Steps) != 2 || plan.Steps[1].Access != AccessEdit {
		t.Errorf("plan = %+v", plan)
	}
	if !strings.Contains(notes, "README.md") {
		t.Errorf("notes = %q", notes)
	}
	if len(llm.jsonCalls) != 1 {
		t.Fatalf("json calls = %d", len(llm.jsonCalls))
	}
	sent := joined(llm.jsonCalls[0])
	for _, want := range []string{"document X", "README.md documents X"} {
		if !strings.Contains(sent, want) {
			t.Errorf("plan request missing %q:\n%s", want, sent)
		}
	}
}

func TestReviseSkipsExploration(t *testing.T) {
	llm := &fakeLLM{jsons: []string{twoSteps}}
	history := taskHistory("document X", "notes here")
	history = append(history, ollama.Message{Role: "assistant", Content: "old plan"})
	plan, err := newPlanner(llm).Revise(context.Background(), history, "also update the changelog")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 {
		t.Errorf("plan = %+v", plan)
	}
	if len(llm.chatCalls) != 0 {
		t.Errorf("chat calls = %d, want 0", len(llm.chatCalls))
	}
	sent := joined(llm.jsonCalls[0])
	for _, want := range []string{"document X", "notes here", "old plan", "also update the changelog"} {
		if !strings.Contains(sent, want) {
			t.Errorf("revise request missing %q", want)
		}
	}
}

func TestDraftRejectsEmptyPlan(t *testing.T) {
	llm := &fakeLLM{jsons: []string{`{"steps":[]}`}}
	if _, _, err := newPlanner(llm).Draft(context.Background(), "x"); err == nil {
		t.Fatal("expected error for a plan with no steps")
	}
}

func TestDraftRetriesWhenAGoalPathIsMissing(t *testing.T) {
	wrong := `{"steps":[{"title":"Edit","instructions":"list them in docs/usage.md","access":"edit"}]}`
	right := `{"steps":[{"title":"Write","instructions":"list them in docs/usages.md","access":"edit"}]}`
	llm := &fakeLLM{jsons: []string{wrong, right}}
	plan, _, err := newPlanner(llm).Draft(context.Background(), "find greet.Hello uses and list them in docs/usages.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.jsonCalls) != 2 || !strings.Contains(joined(llm.jsonCalls[1]), "Use the path docs/usages.md exactly") {
		t.Fatalf("json calls = %d, retry = %q", len(llm.jsonCalls), joined(llm.jsonCalls[len(llm.jsonCalls)-1]))
	}
	if plan.Steps[0].Title != "Write" || plan.Goal == "" {
		t.Errorf("plan = %+v", plan)
	}
}

func TestMissingPaths(t *testing.T) {
	p := Plan{Steps: []Step{{Title: "t", Instructions: "update README.md"}}}
	got := missingPaths("call greet.Hello, update README.md and write CHANGELOG.md.", p)
	if len(got) != 1 || got[0] != "CHANGELOG.md" {
		t.Errorf("missing = %q", got)
	}
}

func TestPlanProblemsAsksToMergeReadSteps(t *testing.T) {
	p := Plan{Steps: []Step{{Title: "a", Access: AccessRead}, {Title: "b", Access: AccessRead}, {Title: "c", Access: AccessEdit}}}
	got := planProblems("document X", p)
	if len(got) != 1 || !strings.Contains(got[0], "Step 1, 2 only reads") {
		t.Errorf("problems = %q", got)
	}
	p.Steps = p.Steps[:2]
	if got := planProblems("find X", p); len(got) != 0 {
		t.Errorf("a read-only goal: problems = %q", got)
	}
}

func TestMissingPathsIgnoresNonFiles(t *testing.T) {
	goal := "e.g. update docs/guide.md, i.e. the guide from github.com; also notes.txt and the config.yaml"
	got := missingPaths(goal, Plan{Steps: []Step{{Title: "t", Instructions: "edit config.yaml"}}})
	if want := []string{"docs/guide.md", "notes.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missing = %q, want %q", got, want)
	}
}

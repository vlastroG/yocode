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

func TestRememberAddsToHistory(t *testing.T) {
	llm := &fakeLLM{replies: []ollama.Message{reply("the security one")}}
	a := newAgent(llm, &fakeTools{})
	a.Remember("/review main feature", "# Review of main...feature")
	if _, err := a.Ask(context.Background(), "which finding is worst?"); err != nil {
		t.Fatal(err)
	}
	msgs := llm.calls[0]
	if !reflect.DeepEqual(msgs[1], ollama.Message{Role: "user", Content: "/review main feature"}) ||
		!reflect.DeepEqual(msgs[2], ollama.Message{Role: "assistant", Content: "# Review of main...feature"}) {
		t.Errorf("history = %+v", msgs[1:3])
	}
}

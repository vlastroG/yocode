package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/deepseek"
)

func TestLoopRespectsMaxSteps(t *testing.T) {
	llm := &fakeLLM{replies: []deepseek.Message{toolCall("git_status"), toolCall("git_status"), toolCall("git_status")}}
	l := Loop{LLM: llm, Model: "m", Tools: &fakeTools{}, MaxSteps: 2}
	if _, err := l.Run(context.Background(), []deepseek.Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected step limit error")
	}
	if len(llm.calls) != 2 {
		t.Errorf("model calls = %d, want 2", len(llm.calls))
	}
}

func TestLoopWrapupAnswersWithoutTools(t *testing.T) {
	llm := &fakeLLM{replies: []deepseek.Message{toolCall("git_status"), toolCall("git_status"),
		{Role: "assistant", Content: "notes"}}}
	l := Loop{LLM: llm, Model: "m", Tools: &fakeTools{}, MaxSteps: 2, Wrapup: "answer now"}
	out, err := l.Run(context.Background(), []deepseek.Message{{Role: "user", Content: "hi"}})
	if err != nil || out != "notes" {
		t.Fatalf("Run = %q, %v", out, err)
	}
	last := llm.calls[2]
	if last[len(last)-1].Content != "answer now" || llm.tools[2] != 0 {
		t.Errorf("wrap-up call: last message %q, %d tools", last[len(last)-1].Content, llm.tools[2])
	}
}

func TestLoopReportsInvalidArgumentsToModel(t *testing.T) {
	bad := deepseek.Message{Role: "assistant", ToolCalls: []deepseek.ToolCall{{ID: "c1", Type: "function",
		Function: deepseek.ToolCallFunction{Name: "git_status", Raw: "{oops", ArgsErr: errors.New("arguments are not a valid JSON object")}}}}
	llm := &fakeLLM{replies: []deepseek.Message{bad, reply("done")}}
	tools := &fakeTools{}
	ans, err := Loop{LLM: llm, Model: "m", Tools: tools}.Run(context.Background(), nil)
	if err != nil || ans != "done" {
		t.Fatalf("ans=%q err=%v", ans, err)
	}
	if len(tools.called) != 0 {
		t.Errorf("tool ran with invalid arguments: %v", tools.called)
	}
	last := llm.calls[1][len(llm.calls[1])-1]
	if last.Role != "tool" || last.ToolCallID != "c1" || !strings.Contains(last.Content, "not a valid JSON") {
		t.Errorf("tool message = %+v", last)
	}
}

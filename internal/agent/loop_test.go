package agent

import (
	"context"
	"testing"

	"github.com/vlastroG/yocode/internal/ollama"
)

func TestLoopRespectsMaxSteps(t *testing.T) {
	llm := &fakeLLM{replies: []ollama.Message{toolCall("git_status"), toolCall("git_status"), toolCall("git_status")}}
	l := Loop{LLM: llm, Model: "m", Tools: &fakeTools{}, MaxSteps: 2}
	if _, err := l.Run(context.Background(), []ollama.Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected step limit error")
	}
	if len(llm.calls) != 2 {
		t.Errorf("model calls = %d, want 2", len(llm.calls))
	}
}

func TestLoopWrapupAnswersWithoutTools(t *testing.T) {
	llm := &fakeLLM{replies: []ollama.Message{toolCall("git_status"), toolCall("git_status"),
		{Role: "assistant", Content: "notes"}}}
	l := Loop{LLM: llm, Model: "m", Tools: &fakeTools{}, MaxSteps: 2, Wrapup: "answer now"}
	out, err := l.Run(context.Background(), []ollama.Message{{Role: "user", Content: "hi"}})
	if err != nil || out != "notes" {
		t.Fatalf("Run = %q, %v", out, err)
	}
	last := llm.calls[2]
	if last[len(last)-1].Content != "answer now" || llm.tools[2] != 0 {
		t.Errorf("wrap-up call: last message %q, %d tools", last[len(last)-1].Content, llm.tools[2])
	}
}

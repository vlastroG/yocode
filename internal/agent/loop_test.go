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

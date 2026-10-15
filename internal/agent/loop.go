package agent

import (
	"context"
	"fmt"

	"github.com/vlastroG/yocode/internal/ollama"
)

const defaultMaxSteps = 6

// Loop runs the model/tool conversation shared by questions, planning and task steps.
type Loop struct {
	LLM        LLM
	Model      string
	Tools      Tools
	MaxSteps   int // model calls; 0 means 6
	OnToolCall func(name string, args map[string]any)
	// Wrapup, if set, is sent when the steps run out, and the model answers once without tools
	// instead of the run failing.
	Wrapup string
}

// Run lists the tools, then calls the model and runs its tool calls until it answers without one.
func (l Loop) Run(ctx context.Context, msgs []ollama.Message) (string, error) {
	defs, err := l.Tools.Tools(ctx)
	if err != nil {
		return "", fmt.Errorf("list tools: %w", err)
	}
	steps := l.MaxSteps
	if steps <= 0 {
		steps = defaultMaxSteps
	}
	for range steps {
		resp, err := l.LLM.Chat(ctx, l.Model, msgs, defs)
		if err != nil {
			return "", err
		}
		msgs = append(msgs, resp)
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}
		for _, tc := range resp.ToolCalls {
			if l.OnToolCall != nil {
				l.OnToolCall(tc.Function.Name, tc.Function.Arguments)
			}
			out, err := l.Tools.Call(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				out = "error: " + err.Error()
			}
			msgs = append(msgs, ollama.Message{Role: "tool", Content: out, ToolName: tc.Function.Name})
		}
	}
	if l.Wrapup != "" {
		resp, err := l.LLM.Chat(ctx, l.Model, append(msgs, ollama.Message{Role: "user", Content: l.Wrapup}), nil)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}
	return "", fmt.Errorf("no answer after %d tool steps", steps)
}

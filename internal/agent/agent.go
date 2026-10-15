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
- Use grep to find where identifiers are used before reading files.
- Mention the documentation source paths you relied on.
- Answer in the language of the user's question, even if the documentation is in another language.
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

	history  []ollama.Message
}

func (a *Agent) Ask(ctx context.Context, question string) (string, error) {
	hits, err := a.Docs.Search(ctx, question, docHits)
	if err != nil {
		return "", fmt.Errorf("search docs: %w", err)
	}

	msgs := []ollama.Message{{Role: "system", Content: systemPrompt}}
	msgs = append(msgs, a.history...)
	msgs = append(msgs, ollama.Message{Role: "user", Content: withDocs(question, hits)})

	loop := Loop{LLM: a.LLM, Model: a.Model, Tools: a.Tools, MaxSteps: maxSteps, OnToolCall: a.OnToolCall}
	answer, err := loop.Run(ctx, msgs)
	if err != nil {
		return "", err
	}
	a.Remember(question, answer)
	return answer, nil
}

// Remember adds an exchange to the conversation history: Ask uses it for plain question/answer
// pairs, and the UI adds review reports so follow-up questions can refer to them.
func (a *Agent) Remember(question, answer string) {
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

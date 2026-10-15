package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/rag"
)

const exploreSystemPrompt = `You prepare the plan for a task in a software repository. Do not plan yet: explore.
- Use grep, list_files and read_file to find the files, identifiers and documentation the goal is about.
- For goals about the history or recent changes, use git_log and git_diff.
- Paths are relative to the project root. You cannot modify anything.
- You have only a few tool calls: never repeat one, and stop as soon as you know the files involved.
- Reply with short notes: the relevant file paths and the facts about them that matter for the goal.`

const exploreWrapup = "Stop exploring. Reply now with your notes."

const planSystemPrompt = `You write the step-by-step plan for a task in a software repository. Another agent carries out each step.
- Steps are concrete and name the files they read or change. Use the exact file paths the goal names;
  a file that doesn't exist yet is created in an "edit" step.
- A step that changes files reads what it needs itself, so don't plan separate steps that only read.
- Change only the files the goal asks for.
- Each step must be doable on its own by an agent that has only the step's access:
  "read" reads files and git history, "edit" also writes, edits and deletes files, "git" also creates branches and commits.
- Use "git" only when the goal asks to create a branch or commit.
- Never plan git pull or git push: the user does that.
- At most 8 steps. Fewer is better.
- Write titles and instructions in the language of the goal.
- When the user corrects a plan, return the whole corrected plan.`

const (
	exploreSteps = 6
	planDocHits  = 5
)

var planSchema = json.RawMessage(`{"type":"object","required":["steps"],"properties":{"steps":{"type":"array","items":` +
	`{"type":"object","required":["title","instructions","access"],"properties":{"title":{"type":"string"},` +
	`"instructions":{"type":"string"},"access":{"type":"string","enum":["read","edit","git"]}}}}}}`)

type LLM interface {
	agent.LLM
	ChatJSON(ctx context.Context, model string, msgs []ollama.Message, schema json.RawMessage, out any) error
}

type Planner struct {
	LLM   LLM
	Model string
	Tools agent.Tools // read-only view
	Docs  agent.Retriever
	// OnToolCall, if set, is called before each exploration tool call.
	OnToolCall func(name string, args map[string]any)
}

// Draft explores the repository for notes, then turns the goal, docs and notes into a plan.
func (p *Planner) Draft(ctx context.Context, goal string) (Plan, string, error) {
	hits, err := p.Docs.Search(ctx, goal, planDocHits)
	if err != nil {
		return Plan{}, "", fmt.Errorf("search docs: %w", err)
	}
	loop := agent.Loop{LLM: p.LLM, Model: p.Model, Tools: p.Tools, MaxSteps: exploreSteps, OnToolCall: p.OnToolCall,
		Wrapup: exploreWrapup}
	notes, err := loop.Run(ctx, []ollama.Message{
		{Role: "system", Content: exploreSystemPrompt},
		{Role: "user", Content: docsText(hits) + "Goal: " + goal},
	})
	if err != nil {
		return Plan{}, "", fmt.Errorf("explore: %w", err)
	}
	history := taskHistory(goal, notes)
	history[0].Content = docsText(hits) + history[0].Content
	plan, err := p.plan(ctx, history)
	if err != nil {
		return Plan{}, "", err
	}
	plan.Goal = goal
	return plan, notes, nil
}

// Revise replans from the task history (goal, notes, then plan/correction pairs) and the new
// feedback, without exploring again. The caller sets the plan's Goal.
func (p *Planner) Revise(ctx context.Context, history []ollama.Message, feedback string) (Plan, error) {
	msgs := append(append([]ollama.Message(nil), history...), ollama.Message{Role: "user", Content: feedback})
	return p.plan(ctx, msgs)
}

func (p *Planner) plan(ctx context.Context, history []ollama.Message) (Plan, error) {
	msgs := append([]ollama.Message{{Role: "system", Content: planSystemPrompt}}, history...)
	var plan Plan
	if err := p.LLM.ChatJSON(ctx, p.Model, msgs, planSchema, &plan); err != nil {
		return Plan{}, fmt.Errorf("plan: %w", err)
	}
	if err := validate(plan); err != nil {
		return Plan{}, fmt.Errorf("invalid plan: %w", err)
	}
	return plan, nil
}

// taskHistory starts the planning conversation that corrections are appended to.
func taskHistory(goal, notes string) []ollama.Message {
	return []ollama.Message{
		{Role: "user", Content: "Goal: " + goal},
		{Role: "user", Content: "Notes from exploring the repository:\n" + notes},
	}
}

func docsText(hits []rag.Hit) string {
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Documentation excerpts (may not all be relevant):\n\n")
	for i, h := range hits {
		fmt.Fprintf(&b, "[%d] %s\n\n", i+1, h.Text)
	}
	return b.String()
}

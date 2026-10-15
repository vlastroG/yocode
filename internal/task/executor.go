package task

import (
	"context"
	"fmt"
	"strings"

	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/deepseek"
)

const stepRules = `- Use the tools yourself. Never ask the user to do anything.
- Paths are relative to the project root. Never repeat a tool call that already returned a result.
- Tool calls are limited: read only what the step needs, then make the change.
- Read a file before editing it. Prefer edit_file over rewriting a whole file; create new files with write_file.
- A file the plan names that doesn't exist yet is to be created: never change a similarly named file instead.
- Text you write from tool output keeps its exact wording: quote commit messages, identifiers, paths and line numbers.
- When committing, pass git_commit the paths of the files the task changed.
- Never pull or push: the user does that.
- Finish with a short summary of what you changed (file paths) or what you found.`

const stepSystemPrompt = "You carry out one step of an approved plan in a software repository.\n" + stepRules

const readWrapup = "Stop using tools. Reply now with what you found."

const singleSystemPrompt = "You carry out every step of an approved plan in a software repository, in order.\n" + stepRules

const rootSystemPrompt = `You report the result of a task that agents carried out in a software repository.
- Answer in the language of the goal.
- Say what was done, which files changed, and anything left undone or failed.
- Base the report only on the step results and the git status. Be concise.`

const (
	stepMaxSteps = 12
	maxSummary   = 1500 // chars of a step's final message kept for later steps and the report
)

type Executor struct {
	LLM      agent.LLM
	Model    string
	ToolsFor func(Access) agent.Tools // the tools a step with that access may use
	Status   agent.Tools              // used for git_status in the root call
	Single   bool                     // run all steps in one loop instead of one subagent per step
	// OnProgress and OnToolCall, if set, report the current step and its tool calls.
	OnProgress func(string)
	OnToolCall func(name string, args map[string]any)
}

type StepResult struct {
	Step    Step
	Summary string
	Err     string
}

type Result struct {
	Plan   Plan
	Steps  []StepResult
	Answer string
}

// Run carries out the plan, stopping at the first failed step, then writes the root answer.
// Only the root call's failure is returned as an error; step failures are in the Result.
func (e *Executor) Run(ctx context.Context, p Plan) (Result, error) {
	res := Result{Plan: p}
	if e.Single {
		res.Steps = []StepResult{e.runAll(ctx, p)}
	} else {
		for i, s := range p.Steps {
			e.progress(fmt.Sprintf("step %d/%d: %s", i+1, len(p.Steps), s.Title))
			sr := e.runStep(ctx, p, i, res.Steps)
			res.Steps = append(res.Steps, sr)
			if sr.Err != "" {
				break
			}
		}
	}
	e.progress("writing the result")
	answer, err := e.root(ctx, res)
	if err != nil {
		return res, fmt.Errorf("task result: %w", err)
	}
	res.Answer = answer
	return res, nil
}

func (e *Executor) runStep(ctx context.Context, p Plan, i int, done []StepResult) StepResult {
	s := p.Steps[i]
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n\n%s\n", p.Goal, p.markdown(i))
	if len(done) > 0 {
		b.WriteString("Results of the earlier steps:\n")
		for j, d := range done {
			fmt.Fprintf(&b, "%d. %s: %s\n", j+1, d.Step.Title, d.Summary)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Your step: %d. %s\n%s\n\n%s", i+1, s.Title, s.Instructions, s.Access.note())
	return e.loop(ctx, s, stepSystemPrompt, b.String(), stepMaxSteps)
}

func (e *Executor) runAll(ctx context.Context, p Plan) StepResult {
	e.progress(fmt.Sprintf("running %d steps", len(p.Steps)))
	s := Step{Title: "All steps", Instructions: "carry out the whole plan", Access: p.widestAccess()}
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n\n%s\nCarry out these steps in order:\n", p.Goal, p.Markdown())
	for i, st := range p.Steps {
		fmt.Fprintf(&b, "%d. %s (%s): %s\n", i+1, st.Title, st.Access, st.Instructions)
	}
	b.WriteString("\n" + s.Access.note())
	return e.loop(ctx, s, singleSystemPrompt, b.String(), stepMaxSteps*len(p.Steps))
}

func (e *Executor) loop(ctx context.Context, s Step, system, user string, maxSteps int) StepResult {
	l := agent.Loop{LLM: e.LLM, Model: e.Model, Tools: e.ToolsFor(s.Access), MaxSteps: maxSteps, OnToolCall: e.OnToolCall}
	if s.Access == AccessRead {
		// a read step's findings are useful even when it runs out of calls
		l.Wrapup = readWrapup
	}
	out, err := l.Run(ctx, []deepseek.Message{{Role: "system", Content: system}, {Role: "user", Content: user}})
	if err != nil {
		return StepResult{Step: s, Err: err.Error()}
	}
	return StepResult{Step: s, Summary: cut(out, maxSummary)}
}

func (e *Executor) root(ctx context.Context, r Result) (string, error) {
	status, err := e.Status.Call(ctx, "git_status", nil)
	if err != nil {
		status = "git status unavailable: " + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n\nStep results:\n", r.Plan.Goal)
	for i, s := range r.Steps {
		if s.Err != "" {
			fmt.Fprintf(&b, "%d. %s: FAILED: %s\n", i+1, s.Step.Title, s.Err)
		} else {
			fmt.Fprintf(&b, "%d. %s: %s\n", i+1, s.Step.Title, s.Summary)
		}
	}
	if !e.Single {
		for i := len(r.Steps); i < len(r.Plan.Steps); i++ {
			fmt.Fprintf(&b, "%d. %s: did not run because an earlier step failed\n", i+1, r.Plan.Steps[i].Title)
		}
	}
	fmt.Fprintf(&b, "\ngit status:\n%s", status)
	resp, err := e.LLM.Chat(ctx, e.Model, []deepseek.Message{
		{Role: "system", Content: rootSystemPrompt},
		{Role: "user", Content: b.String()},
	}, nil)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (e *Executor) progress(s string) {
	if e.OnProgress != nil {
		e.OnProgress(s)
	}
}

// Markdown renders the answer, each step's outcome and, for a saved plan, how to replay it.
func (r Result) Markdown() string {
	var b strings.Builder
	b.WriteString(r.Answer + "\n\n### Steps\n\n")
	for _, s := range r.Steps {
		if s.Err != "" {
			fmt.Fprintf(&b, "- ✗ **%s** - %s\n", s.Step.Title, s.Err)
		} else {
			fmt.Fprintf(&b, "- ✓ **%s** - %s\n", s.Step.Title, s.Summary)
		}
	}
	if r.Plan.ID != "" {
		fmt.Fprintf(&b, "\nPlan %s: replay with `task --plan %s`\n", r.Plan.ID, r.Plan.ID)
	}
	return b.String()
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

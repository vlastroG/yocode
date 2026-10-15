package task

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/deepseek"
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
- Steps are concrete and name the files they read or change. Use the exact file paths the goal names,
  even when a file with a similar name exists; a file that doesn't exist yet is created in an "edit" step.
- Instructions say what to produce and which tool output to base it on (e.g. git_log, grep);
  don't write the file's content into them.
- A step that changes files reads what it needs itself. Plan a "read" step only when the goal changes nothing.
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
	planRetries  = 2 // replans when the plan breaks a rule planProblems checks
	planDocHits  = 5
)

var planSchema = json.RawMessage(`{"type":"object","required":["steps"],"properties":{"steps":{"type":"array","items":` +
	`{"type":"object","required":["title","instructions","access"],"properties":{"title":{"type":"string"},` +
	`"instructions":{"type":"string"},"access":{"type":"string","enum":["read","edit","git"]}}}}}}`)

type LLM interface {
	agent.LLM
	ChatJSON(ctx context.Context, model string, msgs []deepseek.Message, schema json.RawMessage, out any) error
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
	notes, err := loop.Run(ctx, []deepseek.Message{
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
	// the model sometimes ignores planning rules; pointing at the broken ones usually fixes them
	for range planRetries {
		problems := planProblems(goal, plan)
		if len(problems) == 0 {
			break
		}
		retry := append(history[:len(history):len(history)], deepseek.Message{Role: "assistant", Content: plan.Markdown()},
			deepseek.Message{Role: "user", Content: "Fix the plan and return all of it:\n- " + strings.Join(problems, "\n- ")})
		fixed, err := p.plan(ctx, retry)
		if err != nil {
			break
		}
		plan = fixed
		plan.Goal = goal
	}
	return plan, notes, nil
}

// planProblems lists the planning rules a plan breaks that code can check.
func planProblems(goal string, p Plan) []string {
	var problems []string
	for _, path := range missingPaths(goal, p) {
		problems = append(problems, fmt.Sprintf("Use the path %s exactly as the goal names it, even if it doesn't "+
			"exist yet (then a step creates it) or a file with a similar name exists.", path))
	}
	if p.widestAccess() != AccessRead {
		var reads []string
		for i, s := range p.Steps {
			if s.Access == AccessRead {
				reads = append(reads, fmt.Sprint(i+1))
			}
		}
		if len(reads) > 0 {
			problems = append(problems, fmt.Sprintf("Step %s only reads. Merge it into the step that changes files: "+
				"that step reads what it needs itself.", strings.Join(reads, ", ")))
		}
	}
	return problems
}

var (
	goalPath = regexp.MustCompile(`[A-Za-z0-9_][\w./-]*\.([a-z][a-z0-9]{0,4})\b`)
	fileExts = map[string]bool{"md": true, "go": true, "txt": true, "json": true, "yaml": true, "yml": true,
		"toml": true, "cs": true, "py": true, "js": true, "ts": true}
)

// missingPaths returns the file paths named in the goal that no step mentions. A path has a "/"
// or a known extension, so "e.g" or "github.com" don't count.
func missingPaths(goal string, p Plan) []string {
	var text strings.Builder
	for _, s := range p.Steps {
		text.WriteString(s.Title + "\n" + s.Instructions + "\n")
	}
	var missing []string
	for _, m := range goalPath.FindAllStringSubmatch(goal, -1) {
		path := m[0]
		if !strings.Contains(path, "/") && !fileExts[m[1]] {
			continue
		}
		if !strings.Contains(text.String(), path) {
			missing = append(missing, path)
		}
	}
	return missing
}

// Revise replans from the task history (notes, goal, then plan/correction pairs) and the new
// feedback, without exploring again. The caller sets the plan's Goal.
func (p *Planner) Revise(ctx context.Context, history []deepseek.Message, feedback string) (Plan, error) {
	msgs := append(append([]deepseek.Message(nil), history...), deepseek.Message{Role: "user", Content: feedback})
	return p.plan(ctx, msgs)
}

func (p *Planner) plan(ctx context.Context, history []deepseek.Message) (Plan, error) {
	msgs := append([]deepseek.Message{{Role: "system", Content: planSystemPrompt}}, history...)
	var plan Plan
	if err := p.LLM.ChatJSON(ctx, p.Model, msgs, planSchema, &plan); err != nil {
		return Plan{}, fmt.Errorf("plan: %w", err)
	}
	if err := validate(plan); err != nil {
		return Plan{}, fmt.Errorf("invalid plan: %w", err)
	}
	return plan, nil
}

// taskHistory starts the planning conversation that corrections are appended to. The goal comes
// last, so the plan follows it rather than the notes (e.g. a file the goal names that doesn't exist yet).
func taskHistory(goal, notes string) []deepseek.Message {
	return []deepseek.Message{
		{Role: "user", Content: "Notes from exploring the repository:\n" + notes},
		{Role: "user", Content: "Goal: " + goal + "\n\nPlan this goal. Use the file paths it names exactly."},
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

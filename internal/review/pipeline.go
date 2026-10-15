// Package review reviews the changes of a local branch with the local LLM: one schema-constrained
// call per file diff (with related project docs), then a summary call for cross-file problems.
package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vlastroG/yocode/internal/deepseek"
	"github.com/vlastroG/yocode/internal/rag"
)

const (
	defaultMaxFiles = 25   // reviewable files per run unless Pipeline.MaxFiles or --max-files says otherwise
	maxDiffChars    = 8000 // per LLM call, so each review stays focused on one part of a file
	docHits         = 3
)

type LLM interface {
	ChatJSON(ctx context.Context, model string, msgs []deepseek.Message, schema json.RawMessage, out any) error
}

type Tools interface {
	Call(ctx context.Context, name string, args map[string]any) (string, error)
}

type Retriever interface {
	Search(ctx context.Context, query string, n int) ([]rag.Hit, error)
}

type Pipeline struct {
	LLM      LLM
	Model    string
	Tools    Tools
	Docs     Retriever
	Language string // language of the report text; empty means English
	MaxFiles int    // reviewable files allowed per run; 0 means 25
	// OnProgress, if set, receives short status lines such as "reviewing a.go (2/5)".
	OnProgress func(string)
}

// Args are the review command's arguments: "<base> [head] [--max-files N]".
type Args struct {
	Base, Head string
	MaxFiles   int // overrides Pipeline.MaxFiles for one run; 0 = no override
}

const usage = "usage: review <base> [head] [--max-files N], e.g. review main feature"

// ParseArgs reads "<base> [head] [--max-files N]" (head defaults to HEAD; --max-files=N works too).
func ParseArgs(args []string) (Args, error) {
	var a Args
	var pos []string
	for i := 0; i < len(args); i++ {
		v, isFlag := strings.CutPrefix(args[i], "--max-files=")
		if args[i] == "--max-files" {
			if i+1 == len(args) {
				return Args{}, errors.New("--max-files needs a number")
			}
			v, isFlag = args[i+1], true
			i++
		}
		if !isFlag {
			pos = append(pos, args[i])
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Args{}, fmt.Errorf("--max-files needs a positive number, got %q", v)
		}
		a.MaxFiles = n
	}
	switch len(pos) {
	case 1:
		a.Base, a.Head = pos[0], "HEAD"
	case 2:
		a.Base, a.Head = pos[0], pos[1]
	default:
		return Args{}, errors.New(usage)
	}
	return a, nil
}

// TooManyFilesError stops a review before any model call when it would review more files than
// allowed, so the user can raise the limit or drop the review.
type TooManyFilesError struct {
	Args  Args
	Files int // files that would be reviewed
	Limit int
}

func (e *TooManyFilesError) Error() string {
	return fmt.Sprintf("%s...%s changes %d files to review, more than the limit of %d, so nothing was reviewed. "+
		"Each file takes at least one model call (seconds each on a GPU, much longer on a CPU). "+
		"To review them all, run: review %s %s --max-files %d (or raise REVIEW_MAX_FILES in .env).",
		e.Args.Base, e.Args.Head, e.Files, e.Limit, e.Args.Base, e.Args.Head, e.Files)
}

// Run reviews the changes of head since it branched off base (git's base...head). If more files
// would be reviewed than the limit, it returns a *TooManyFilesError before any model call.
func (p *Pipeline) Run(ctx context.Context, a Args) (*Report, error) {
	rng := a.Base + "..." + a.Head
	out, err := p.Tools.Call(ctx, "git_changed_files", map[string]any{"ref": rng})
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}
	changes := parseChanges(out)
	if len(changes) == 0 {
		return nil, fmt.Errorf("no changes between %s and %s", a.Base, a.Head)
	}

	rep := &Report{Range: rng}
	var todo []fileChange
	for _, c := range changes {
		if reason := skipReason(c); reason != "" {
			rep.Skipped = append(rep.Skipped, Skipped{c.Path, reason})
		} else {
			todo = append(todo, c)
		}
	}
	if limit := p.limit(a); len(todo) > limit {
		return nil, &TooManyFilesError{Args: a, Files: len(todo), Limit: limit}
	}

	commits, err := p.Tools.Call(ctx, "git_log", map[string]any{"ref": a.Base + ".." + a.Head, "n": 50})
	if err != nil {
		return nil, fmt.Errorf("list commits: %w", err)
	}

	for i, c := range todo {
		p.progress(fmt.Sprintf("reviewing %s (%d/%d)", c.Path, i+1, len(todo)))
		findings, reviewed, err := p.reviewFile(ctx, rng, commits, c)
		switch {
		case err != nil:
			rep.Skipped = append(rep.Skipped, Skipped{c.Path, "review failed: " + err.Error()})
		case !reviewed:
			rep.Skipped = append(rep.Skipped, Skipped{c.Path, "no text changes"})
		default:
			rep.Reviewed = append(rep.Reviewed, c.Path)
			rep.Findings = append(rep.Findings, findings...)
		}
	}

	p.progress("summarizing")
	if err := p.summarize(ctx, rep, commits); err != nil {
		return nil, fmt.Errorf("summarize: %w", err)
	}
	rep.Findings = sortFindings(rep.Findings)
	return rep, nil
}

func (p *Pipeline) reviewFile(ctx context.Context, rng, commits string, c fileChange) ([]Finding, bool, error) {
	diff, err := p.Tools.Call(ctx, "git_diff", map[string]any{"ref": rng, "path": c.Path})
	if err != nil {
		return nil, false, err
	}
	numbered := numberDiff(diff)
	if numbered == "" {
		return nil, false, nil
	}
	hits, err := p.Docs.Search(ctx, c.Path+"\n"+truncate(numbered, 600), docHits)
	if err != nil {
		return nil, false, err
	}
	parts := splitDiff(numbered, maxDiffChars)
	var all []Finding
	for i, part := range parts {
		var out struct {
			Findings []Finding `json:"findings"`
		}
		msgs := []deepseek.Message{
			{Role: "system", Content: p.withLanguage(fileSystemPrompt)},
			{Role: "user", Content: fileRequest(rng, commits, c, hits, part, i+1, len(parts))},
		}
		if err := p.LLM.ChatJSON(ctx, p.Model, msgs, fileSchema, &out); err != nil {
			return nil, true, err
		}
		for _, f := range out.Findings {
			f.File = c.Path
			all = append(all, f)
		}
	}
	return all, true, nil
}

func (p *Pipeline) summarize(ctx context.Context, rep *Report, commits string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Pull request %s\n\nCommits:\n%s\n\nChanged files:\n", rep.Range, commits)
	for _, f := range rep.Reviewed {
		b.WriteString("- " + f + "\n")
	}
	for _, s := range rep.Skipped {
		fmt.Fprintf(&b, "- %s (not reviewed: %s)\n", s.Path, s.Reason)
	}
	b.WriteString("\nFindings of the per-file review:\n")
	if len(rep.Findings) == 0 {
		b.WriteString("(none)\n")
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(&b, "- [%s/%s] %s — %s\n", f.Severity, f.Category, location(f), f.Message)
	}

	var out struct {
		Summary  string    `json:"summary"`
		Findings []Finding `json:"findings"`
	}
	msgs := []deepseek.Message{
		{Role: "system", Content: p.withLanguage(summarySystemPrompt)},
		{Role: "user", Content: b.String()},
	}
	if err := p.LLM.ChatJSON(ctx, p.Model, msgs, summarySchema, &out); err != nil {
		return err
	}
	rep.Summary = out.Summary
	rep.Findings = append(rep.Findings, out.Findings...)
	return nil
}

func (p *Pipeline) limit(a Args) int {
	switch {
	case a.MaxFiles > 0:
		return a.MaxFiles
	case p.MaxFiles > 0:
		return p.MaxFiles
	}
	return defaultMaxFiles
}

func (p *Pipeline) progress(s string) {
	if p.OnProgress != nil {
		p.OnProgress(s)
	}
}

func (p *Pipeline) withLanguage(prompt string) string {
	lang := p.Language
	if lang == "" {
		lang = "English"
	}
	return prompt + "\nWrite all text in " + lang + "."
}

func fileRequest(rng, commits string, c fileChange, hits []rag.Hit, diff string, part, parts int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pull request %s\n\nCommits:\n%s\n\n", rng, commits)
	if len(hits) > 0 {
		b.WriteString("Project documentation that may be relevant:\n\n")
		for i, h := range hits {
			fmt.Fprintf(&b, "[%d] %s\n\n", i+1, h.Text)
		}
	}
	fmt.Fprintf(&b, "File: %s (%s)\n", c.Path, statusName(c.Status))
	if parts > 1 {
		fmt.Fprintf(&b, "Diff part %d of %d:\n", part, parts)
	} else {
		b.WriteString("Diff:\n")
	}
	b.WriteString(diff)
	return b.String()
}

func statusName(s string) string {
	switch s {
	case "A":
		return "added"
	case "M":
		return "modified"
	case "R":
		return "renamed"
	case "C":
		return "copied"
	case "T":
		return "type changed"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

const fileSystemPrompt = `You are a senior code reviewer. You review one file of a pull request at a time and report problems in the changed lines (lines marked with + in the diff).
Categories:
- bug: logic errors, wrong conditions, unhandled errors, nil or index problems, race conditions, resource leaks
- security: injection, path traversal, secrets in code, unsafe handling of input, missing authorization
- architecture: code in the wrong layer, tight coupling, duplicating existing functionality, breaking existing contracts or APIs
- docs: the change makes the project documentation shown below outdated, or contradicts conventions described there
- recommendation: a concrete improvement worth making; not formatting or naming nitpicks
Severity: high = must fix before merging, medium = should fix, low = nice to have.
Rules:
- Each diff line starts with its line number in the new file; put that number in "line".
- Each message must say what is wrong and why. Put the fix in "suggestion".
- Only report real problems you can point to in this diff. If there are none, return an empty "findings" list.`

const summarySystemPrompt = `You are a senior engineer writing the overall review of a pull request. You get the commits, the changed files and the findings of a per-file review.
Return:
- "summary": 3 to 6 sentences: what the pull request does, its overall risk, and the most important things to fix before merging.
- "findings": only NEW problems that the per-file review could not see because they span several files: architectural problems (wrong layering, duplicated functionality, inconsistent changes across files) or documentation that must be updated. Leave "file" empty for problems of the whole pull request. Do not repeat per-file findings. Return an empty list if there are none.`

const findingProps = `"severity":{"type":"string","enum":["high","medium","low"]},` +
	`"category":{"type":"string","enum":["bug","security","architecture","docs","recommendation"]},` +
	`"message":{"type":"string"},"suggestion":{"type":"string"}`

var fileSchema = json.RawMessage(`{"type":"object","required":["findings"],"properties":{"findings":{"type":"array","items":` +
	`{"type":"object","required":["line","severity","category","message"],"properties":{"line":{"type":"integer"},` + findingProps + `}}}}}`)

var summarySchema = json.RawMessage(`{"type":"object","required":["summary","findings"],"properties":{"summary":{"type":"string"},` +
	`"findings":{"type":"array","items":{"type":"object","required":["severity","category","message"],` +
	`"properties":{"file":{"type":"string"},` + findingProps + `}}}}}`)

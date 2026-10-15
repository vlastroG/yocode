// Package commands defines the actions available both as CLI subcommands (yocode review main)
// and as chat commands (/review main), so the two stay in sync.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/review"
	"github.com/vlastroG/yocode/internal/task"
)

const (
	planHint   = "\n\nType corrections, /go to run it, /cancel to drop it."
	promptHint = "\n\nEnter = run · q = cancel · or type a correction: "
)

type Command struct {
	Name  string
	Args  string // argument syntax for help, e.g. "<base> [head]"
	Help  string
	Label string // title of the result block in the chat
	// ChatOnly commands act on chat state (the pending plan) and are not CLI subcommands.
	ChatOnly bool
	// Check validates the arguments cheaply, so errors show up before anything slow starts.
	Check func(args []string) error
	// Run executes the command and returns its text output.
	Run func(ctx context.Context, args []string) (string, error)

	label func() string // overrides Label after Run
}

// ResultLabel is the title of the block that shows Run's output.
func (c Command) ResultLabel() string {
	if c.label != nil {
		return c.label()
	}
	return c.Label
}

type Set []Command

type Deps struct {
	Agent  *agent.Agent
	Review *review.Pipeline
	Task   *task.Session
	// Tools is the read-only view used by diff.
	Tools agent.Tools
	// Prompt asks the user on the terminal (CLI only); nil in the chat, which uses /go and /cancel.
	Prompt func(text string) (string, error)
}

// New binds the commands to their dependencies. New(Deps{}) still serves names, help and checks.
func New(d Deps) Set {
	a := d.Agent
	return Set{
		{
			Name: "ask", Args: "<question>", Label: "yocode",
			Help: "answer a question about the docs, files or git history",
			Check: func(args []string) error {
				if strings.TrimSpace(strings.Join(args, " ")) == "" {
					return errors.New("usage: ask <question>")
				}
				return nil
			},
			Run: func(ctx context.Context, args []string) (string, error) {
				return a.Ask(ctx, strings.Join(args, " "))
			},
		},
		{
			Name: "review", Args: "<base> [head] [--max-files N]", Label: "review",
			Help: "review the changes of head (default HEAD) since it branched off base",
			Check: func(args []string) error {
				_, err := review.ParseArgs(args)
				return err
			},
			Run: func(ctx context.Context, args []string) (string, error) {
				ra, err := review.ParseArgs(args)
				if err != nil {
					return "", err
				}
				rep, err := d.Review.Run(ctx, ra)
				if err != nil {
					return "", err
				}
				md := rep.Markdown()
				// Follow-up questions in the chat can refer to the report.
				a.Remember("/review "+strings.Join(args, " "), md)
				return md, nil
			},
		},
		{
			Name: "task", Args: "<goal> [--yes] | --plan <id>", Label: "task",
			Help: "plan a goal, correct the plan, then run it step by step; --plan replays a saved plan",
			Check: func(args []string) error {
				_, err := task.ParseArgs(args)
				return err
			},
			Run: func(ctx context.Context, args []string) (string, error) {
				ta, err := task.ParseArgs(args)
				if err != nil {
					return "", err
				}
				return runTask(ctx, d, ta)
			},
			label: d.taskLabel,
		},
		{
			Name: "diff", Args: "[path]", Label: "diff",
			Help: "show uncommitted changes (including new files)",
			Check: func(args []string) error {
				if len(args) > 1 {
					return errors.New("usage: diff [path]")
				}
				return nil
			},
			Run: func(ctx context.Context, args []string) (string, error) {
				path := ""
				if len(args) > 0 {
					path = args[0]
				}
				return WorkingDiff(ctx, d.Tools, path)
			},
		},
		{
			Name: "revise", Args: "<correction>", Label: "plan", ChatOnly: true,
			Help: "correct the pending plan (plain text does the same)",
			Check: func(args []string) error {
				if err := d.checkPending(); err != nil {
					return err
				}
				if strings.TrimSpace(strings.Join(args, " ")) == "" {
					return errors.New("usage: revise <correction>")
				}
				return nil
			},
			Run: func(ctx context.Context, args []string) (string, error) {
				p, err := d.Task.Revise(ctx, strings.Join(args, " "))
				if err != nil {
					return "", err
				}
				return p.Markdown() + planHint, nil
			},
		},
		{
			Name: "go", Label: "task", ChatOnly: true,
			Help:  "run the pending plan",
			Check: func([]string) error { return d.checkPending() },
			Run: func(ctx context.Context, _ []string) (string, error) {
				return d.finish(d.Task.Approve(ctx))
			},
		},
		{
			Name: "cancel", Label: "task", ChatOnly: true,
			Help:  "drop the pending plan",
			Check: func([]string) error { return d.checkPending() },
			Run: func(context.Context, []string) (string, error) {
				d.Task.Cancel()
				return "cancelled", nil
			},
		},
	}
}

func runTask(ctx context.Context, d Deps, ta task.Args) (string, error) {
	if ta.PlanID != "" {
		return d.finish(d.Task.Replay(ctx, ta.PlanID))
	}
	plan, err := d.Task.Start(ctx, ta.Goal)
	if err != nil {
		return "", err
	}
	if ta.Yes {
		return d.finish(d.Task.Approve(ctx))
	}
	if d.Prompt == nil {
		return plan.Markdown() + planHint, nil
	}
	text := plan.Markdown() + promptHint
	for {
		reply, err := d.Prompt(text)
		switch {
		case errors.Is(err, io.EOF):
			d.Task.Cancel()
			return "", errors.New("no input: rerun with --yes to run the plan without asking")
		case err != nil:
			d.Task.Cancel()
			return "", fmt.Errorf("read reply: %w", err)
		case reply == "":
			return d.finish(d.Task.Approve(ctx))
		case reply == "q":
			d.Task.Cancel()
			return "cancelled", nil
		}
		revised, err := d.Task.Revise(ctx, reply)
		if err != nil {
			// the previous plan is still pending, so the user can retry, run or cancel it
			text = fmt.Sprintf("Revision failed: %v\n\n", err) + plan.Markdown() + promptHint
			continue
		}
		plan = revised
		text = plan.Markdown() + promptHint
	}
}

// finish adds a task's result to the chat history so follow-up questions can refer to it.
// A failure after steps ran keeps the step report in the error, so the chat and CLI show both.
func (d Deps) finish(res task.Result, err error) (string, error) {
	if err != nil && len(res.Steps) == 0 {
		return "", err
	}
	md := res.Markdown()
	if err != nil {
		err = fmt.Errorf("%w\n\n%s", err, md)
		md = err.Error()
	}
	d.Agent.Remember("/task "+res.Plan.Goal, md)
	if err != nil {
		return "", err
	}
	return md, nil
}

func (d Deps) checkPending() error {
	if d.Task == nil || !d.Task.Pending() {
		return errors.New("no plan pending: start one with /task <goal>")
	}
	return nil
}

func (d Deps) taskLabel() string {
	if d.Task != nil && d.Task.Pending() {
		return "plan"
	}
	return "task"
}

// CLI returns the commands without the chat-only ones.
func (s Set) CLI() Set {
	var out Set
	for _, c := range s {
		if !c.ChatOnly {
			out = append(out, c)
		}
	}
	return out
}

func (s Set) Lookup(name string) (Command, bool) {
	for _, c := range s {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// Usage lists the commands, one per line, with prefix before each name ("/" in the chat).
func (s Set) Usage(prefix string) string {
	var b strings.Builder
	for _, c := range s {
		b.WriteString(UsageLine(prefix+c.Name+" "+c.Args, c.Help))
	}
	return b.String()
}

// UsageLine formats one help line; callers use it for extra entries such as /help.
func UsageLine(syntax, help string) string {
	return fmt.Sprintf("  %-40s %s\n", syntax, help)
}

// Parse turns chat input into a command call: plain text asks a question, or revises the plan
// while one is pending; "/name args" picks a command. The arguments are checked.
func (s Set) Parse(input string) (Command, []string, error) {
	name, args := "ask", []string{input}
	if rev, ok := s.Lookup("revise"); ok && rev.Check(args) == nil {
		name = "revise"
	}
	if strings.HasPrefix(input, "/") {
		fields := strings.Fields(input)
		name, args = strings.TrimPrefix(fields[0], "/"), fields[1:]
	}
	c, ok := s.Lookup(name)
	if !ok {
		return Command{}, nil, fmt.Errorf("unknown command /%s (type /help)", name)
	}
	if err := c.Check(args); err != nil {
		return Command{}, nil, err
	}
	return c, args, nil
}

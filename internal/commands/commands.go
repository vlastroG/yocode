// Package commands defines the actions available both as CLI subcommands (yocode review main)
// and as chat commands (/review main), so the two stay in sync.
package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/review"
)

type Command struct {
	Name  string
	Args  string // argument syntax for help, e.g. "<base> [head]"
	Help  string
	Label string // title of the result block in the chat
	// Check validates the arguments cheaply, so errors show up before anything slow starts.
	Check func(args []string) error
	// Run executes the command and returns its text output.
	Run func(ctx context.Context, args []string) (string, error)
}

type Set []Command

// New binds the commands to the agent and the review pipeline. Both may be nil when only the
// names, help and argument checks are needed.
func New(a *agent.Agent, rev *review.Pipeline) Set {
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
				rep, err := rev.Run(ctx, ra)
				if err != nil {
					return "", err
				}
				md := rep.Markdown()
				// Follow-up questions in the chat can refer to the report.
				a.Remember("/review "+strings.Join(args, " "), md)
				return md, nil
			},
		},
	}
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

// Parse turns chat input into a command call: plain text asks a question, "/name args" picks a
// command. The arguments are checked.
func (s Set) Parse(input string) (Command, []string, error) {
	name, args := "ask", []string{input}
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

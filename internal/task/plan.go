// Package task turns a high-level goal into a plan the user corrects, then runs it with one
// subagent per step and a root call that reports the result.
package task

import (
	"errors"
	"fmt"
	"strings"
)

const maxPlanSteps = 8

// Access is what a step may do: read the repository, also edit files, or also branch and commit.
type Access string

const (
	AccessRead Access = "read"
	AccessEdit Access = "edit"
	AccessGit  Access = "git"
)

// rank orders accesses by the tools they grant; 0 means unknown.
func (a Access) rank() int {
	switch a {
	case AccessRead:
		return 1
	case AccessEdit:
		return 2
	case AccessGit:
		return 3
	}
	return 0
}

// note tells an agent what its access allows, so a read step doesn't try to write.
func (a Access) note() string {
	switch a {
	case AccessEdit:
		return "Your access is edit: you can read, create, edit and delete files, but not branch or commit."
	case AccessGit:
		return "Your access is git: you can read and change files, create branches and commit."
	}
	return "Your access is read: you can read files and git history, but not change anything."
}

type Step struct {
	Title        string `json:"title"`
	Instructions string `json:"instructions"`
	Access       Access `json:"access"`
}

type Plan struct {
	ID    string `json:"id"` // empty until approved
	Goal  string `json:"goal"`
	Steps []Step `json:"steps"`
}

func (p Plan) Markdown() string { return p.markdown(-1) }

// markdown renders the plan with "→" before step current (0-based; -1 marks none).
func (p Plan) markdown(current int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Plan: %s\n\n", p.Goal)
	for i, s := range p.Steps {
		if i == current {
			b.WriteString("→ ")
		}
		fmt.Fprintf(&b, "%d. **%s** (%s) - %s\n", i+1, s.Title, s.Access, s.Instructions)
	}
	return b.String()
}

func (p Plan) widestAccess() Access {
	widest := AccessRead
	for _, s := range p.Steps {
		if s.Access.rank() > widest.rank() {
			widest = s.Access
		}
	}
	return widest
}

func validate(p Plan) error {
	if len(p.Steps) == 0 {
		return errors.New("the plan has no steps")
	}
	if len(p.Steps) > maxPlanSteps {
		return fmt.Errorf("the plan has %d steps, more than %d", len(p.Steps), maxPlanSteps)
	}
	for i, s := range p.Steps {
		if strings.TrimSpace(s.Title) == "" {
			return fmt.Errorf("step %d has no title", i+1)
		}
		if s.Access.rank() == 0 {
			return fmt.Errorf("step %d has unknown access %q", i+1, s.Access)
		}
	}
	return nil
}

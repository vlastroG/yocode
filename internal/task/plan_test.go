package task

import (
	"strings"
	"testing"
)

func steps(n int) []Step {
	s := make([]Step, n)
	for i := range s {
		s[i] = Step{Title: "t", Instructions: "do it", Access: AccessRead}
	}
	return s
}

func TestValidate(t *testing.T) {
	for _, n := range []int{1, 8} {
		if err := validate(Plan{Steps: steps(n)}); err != nil {
			t.Errorf("%d steps: %v", n, err)
		}
	}
	bad := map[string]Plan{
		"0 steps":        {Steps: steps(0)},
		"9 steps":        {Steps: steps(9)},
		"empty title":    {Steps: []Step{{Title: " ", Access: AccessRead}}},
		"unknown access": {Steps: []Step{{Title: "t", Access: "push"}}},
	}
	for name, p := range bad {
		if err := validate(p); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestPlanMarkdown(t *testing.T) {
	p := Plan{Goal: "update docs", Steps: []Step{{Title: "Edit README", Instructions: "add X", Access: AccessEdit}}}
	md := p.Markdown()
	for _, want := range []string{"## Plan: update docs", "1. **Edit README** (edit) - add X"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

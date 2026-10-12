package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderSeparatesExchanges(t *testing.T) {
	log := []entry{
		{introEntry, "docs index reused"},
		{userEntry, "first question"},
		{toolEntry, "git_status"},
		{toolEntry, "git_log"},
		{botEntry, "first answer " + strings.Repeat("word ", 40)},
		{userEntry, "second question"},
		{errEntry, "boom"},
	}
	out := render(log, 60)

	for _, want := range []string{"You", "yocode", "error", "↳ git_status", "first answer", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if n := strings.Count(out, strings.Repeat("─", 60)); n != 2 {
		t.Errorf("want a rule before each of the 2 questions, got %d", n)
	}
	if strings.Contains(out, "↳ git_status\n\n") {
		t.Error("consecutive tool calls should not be separated by a blank line")
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 60 {
			t.Errorf("line %d is %d columns wide (> 60): %q", i, w, line)
		}
	}
}

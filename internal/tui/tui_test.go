package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/vlastroG/yocode/internal/commands"
)

func TestRenderSeparatesExchanges(t *testing.T) {
	log := []entry{
		{kind: introEntry, text: "docs index reused"},
		{kind: userEntry, text: "first question"},
		{kind: toolEntry, text: "git_status"},
		{kind: toolEntry, text: "git_log"},
		{kind: replyEntry, label: "yocode", text: "first answer " + strings.Repeat("word ", 40)},
		{kind: userEntry, text: "/review main"},
		{kind: replyEntry, label: "review", text: "# Review of main...HEAD"},
		{kind: errEntry, text: "boom"},
		{kind: errEntry, label: "warning", text: "too many files"},
	}
	out := render(log, 60)

	for _, want := range []string{"You", "yocode", "review", "# Review of main...HEAD", "error", "boom", "warning", "too many files", "↳ git_status", "first answer"} {
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

func TestHelpAndSuggestions(t *testing.T) {
	cmds := commands.New(nil, nil)
	h := help(cmds)
	for _, want := range []string{"/ask <question>", "/review <base> [head]", "/help", "/exit"} {
		if !strings.Contains(h, want) {
			t.Errorf("help lacks %q:\n%s", want, h)
		}
	}
	s := strings.Join(suggestions(cmds), "|")
	if !strings.Contains(s, "/review ") || !strings.Contains(s, "/help") {
		t.Errorf("suggestions = %q", s)
	}
}

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type lineKind int

const (
	ctxLine lineKind = iota
	fileLine
	hunkLine
	addLine
	delLine
)

var diffStyles = map[lineKind]lipgloss.Style{
	fileLine: lipgloss.NewStyle().Bold(true),
	hunkLine: lipgloss.NewStyle().Foreground(lipgloss.Color("14")),
	addLine:  lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
	delLine:  lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
	ctxLine:  lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
}

func diffLineKind(line string) lineKind {
	for _, p := range []string{"diff --git", "---", "+++", "new file", "deleted file", "index "} {
		if strings.HasPrefix(line, p) {
			return fileLine
		}
	}
	switch {
	case strings.HasPrefix(line, "@@"):
		return hunkLine
	case strings.HasPrefix(line, "+"):
		return addLine
	case strings.HasPrefix(line, "-"):
		return delLine
	}
	return ctxLine
}

// colorDiff colors each line by kind. Long lines are cut, not wrapped, to keep the +/- column.
func colorDiff(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if width > 0 && lipgloss.Width(l) > width {
			r := []rune(l)
			for len(r) > 0 && lipgloss.Width(string(r)) > width {
				r = r[:len(r)-1]
			}
			l = string(r)
		}
		lines[i] = diffStyles[diffLineKind(l)].Render(l)
	}
	return strings.Join(lines, "\n")
}

// Package tui is a minimal chat interface: scrollback, a spinner while thinking, one input line.
package tui

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vlastroG/yocode/internal/agent"
)

const (
	userColor = lipgloss.Color("12")
	botColor  = lipgloss.Color("10")
	errColor  = lipgloss.Color("9")
)

var (
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	// barStyle draws a colored bar on the left of a message block.
	barStyle = lipgloss.NewStyle().Border(lipgloss.ThickBorder(), false, false, false, true).PaddingLeft(1)
)

type entryKind int

const (
	introEntry entryKind = iota
	userEntry
	toolEntry
	botEntry
	errEntry
)

// entry is one item of the scrollback; it is rendered at the current width on every refresh.
type entry struct {
	kind entryKind
	text string
}

type answerMsg struct {
	text string
	err  error
}

type toolMsg string

type model struct {
	agent *agent.Agent
	input textinput.Model
	view  viewport.Model
	spin  spinner.Model
	log   []entry
	busy  bool
	width int
}

// Run starts the chat UI. intro is shown as the first line (index/model info).
func Run(a *agent.Agent, intro string) error {
	in := textinput.New()
	in.Placeholder = "Ask about the docs, files or git history (/exit to quit)"
	in.Prompt = "> "
	in.CharLimit = 2000
	in.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := model{agent: a, input: in, view: viewport.New(80, 20), spin: sp, log: []entry{{introEntry, intro}}}
	m.refresh()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	a.OnToolCall = func(name string, args map[string]any) { p.Send(toolMsg(formatCall(name, args))) }
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.view.Width = msg.Width
		m.view.Height = max(msg.Height-3, 1)
		m.input.Width = max(msg.Width-4, 10)
		m.refresh()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "pgup", "pgdown":
			var cmd tea.Cmd
			m.view, cmd = m.view.Update(msg)
			return m, cmd
		case "enter":
			q := strings.TrimSpace(m.input.Value())
			if q == "" || m.busy {
				return m, nil
			}
			if q == "/exit" || q == "/quit" {
				return m, tea.Quit
			}
			m.input.Reset()
			m.busy = true
			m.add(userEntry, q)
			return m, tea.Batch(m.spin.Tick, ask(m.agent, q))
		}
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	case toolMsg:
		m.add(toolEntry, string(msg))
		return m, nil
	case answerMsg:
		m.busy = false
		if msg.err != nil {
			m.add(errEntry, msg.err.Error())
		} else {
			m.add(botEntry, msg.text)
		}
		return m, nil
	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	status := dimStyle.Render("enter: ask · pgup/pgdn or mouse: scroll · esc: quit")
	if m.busy {
		status = m.spin.View() + " thinking..."
	}
	return m.view.View() + "\n" + status + "\n" + m.input.View()
}

func (m *model) add(kind entryKind, text string) {
	m.log = append(m.log, entry{kind, text})
	m.refresh()
}

func (m *model) refresh() {
	m.view.SetContent(render(m.log, m.width))
	m.view.GotoBottom()
}

// render lays out the scrollback: each exchange starts with a rule, questions and answers get a
// labelled block with a colored left bar, tool calls are dim lines in between.
func render(log []entry, width int) string {
	width = max(width, 20)
	var b strings.Builder
	for i, e := range log {
		switch {
		case i == 0:
		case e.kind == userEntry:
			b.WriteString("\n\n" + dimStyle.Render(strings.Repeat("─", width)) + "\n\n")
		case e.kind == toolEntry && log[i-1].kind == toolEntry:
			b.WriteString("\n")
		default:
			b.WriteString("\n\n")
		}
		switch e.kind {
		case introEntry:
			b.WriteString(dimStyle.Width(width).Render(e.text))
		case userEntry:
			b.WriteString(block("You", e.text, userColor, width))
		case toolEntry:
			b.WriteString(dimStyle.PaddingLeft(2).Width(width).Render("↳ " + e.text))
		case botEntry:
			b.WriteString(block("yocode", e.text, botColor, width))
		case errEntry:
			b.WriteString(block("error", e.text, errColor, width))
		}
	}
	return b.String()
}

func block(label, text string, color lipgloss.Color, width int) string {
	head := lipgloss.NewStyle().Bold(true).Foreground(color).Render(label)
	// Width covers content + padding; the 1-column bar is added outside it.
	return barStyle.BorderForeground(color).Width(width - 1).Render(head + "\n" + text)
}

func ask(a *agent.Agent, q string) tea.Cmd {
	return func() tea.Msg {
		text, err := a.Ask(context.Background(), q)
		return answerMsg{text: text, err: err}
	}
}

func formatCall(name string, args map[string]any) string {
	b, _ := json.Marshal(args)
	if s := string(b); s != "{}" && s != "null" {
		return name + " " + s
	}
	return name
}

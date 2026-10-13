// Package tui is a minimal chat interface: scrollback, a spinner while working, one input line.
// Input is a question or a /command from the commands package.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/commands"
	"github.com/vlastroG/yocode/internal/review"
)

const (
	userColor  = lipgloss.Color("12")
	replyColor = lipgloss.Color("10")
	errColor   = lipgloss.Color("9")
)

// labelColors gives some command results their own bar color; others use replyColor.
var labelColors = map[string]lipgloss.Color{"review": lipgloss.Color("11"), "warning": lipgloss.Color("11")}

var (
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	// barStyle draws a colored bar on the left of a message block.
	barStyle = lipgloss.NewStyle().Border(lipgloss.ThickBorder(), false, false, false, true).PaddingLeft(1)
)

type entryKind int

const (
	introEntry entryKind = iota // dim text: startup info and /help
	userEntry
	toolEntry // dim progress line: tool calls, review steps
	replyEntry
	errEntry
)

// entry is one item of the scrollback; it is rendered at the current width on every refresh.
type entry struct {
	kind  entryKind
	text  string
	label string // title of a replyEntry block: the command's label
}

type resultMsg struct {
	label string
	text  string
	err   error
}

type toolMsg string

type model struct {
	cmds  commands.Set
	input textinput.Model
	view  viewport.Model
	spin  spinner.Model
	log   []entry
	busy  bool
	width int
}

// Run starts the chat UI. intro is shown first (index/model info), followed by the command list.
func Run(a *agent.Agent, rev *review.Pipeline, intro string) error {
	cmds := commands.New(a, rev)
	in := textinput.New()
	in.Placeholder = "Ask a question, or type / for commands"
	in.Prompt = "> "
	in.CharLimit = 2000
	in.ShowSuggestions = true
	in.SetSuggestions(suggestions(cmds))
	in.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := model{cmds: cmds, input: in, view: viewport.New(80, 20), spin: sp,
		log: []entry{{kind: introEntry, text: intro + "\n\n" + help(cmds)}}}
	m.refresh()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	a.OnToolCall = func(name string, args map[string]any) { p.Send(toolMsg(formatCall(name, args))) }
	rev.OnProgress = func(s string) { p.Send(toolMsg(s)) }
	_, err := p.Run()
	return err
}

// help lists the chat commands, including the built-in ones.
func help(cmds commands.Set) string {
	return "Type a question, or a command (Tab completes):\n" + cmds.Usage("/") +
		commands.UsageLine("/help", "show this list") +
		commands.UsageLine("/exit", "quit")
}

// suggestions are offered for Tab completion while typing a command.
func suggestions(cmds commands.Set) []string {
	s := []string{"/help", "/exit"}
	for _, c := range cmds {
		s = append(s, "/"+c.Name+" ")
	}
	return s
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
			m.input.Reset()
			switch q {
			case "/exit", "/quit":
				return m, tea.Quit
			case "/help":
				m.add(entry{kind: introEntry, text: help(m.cmds)})
				return m, nil
			}
			c, args, err := m.cmds.Parse(q)
			if err != nil {
				m.add(entry{kind: errEntry, text: err.Error()})
				return m, nil
			}
			m.busy = true
			m.add(entry{kind: userEntry, text: q})
			return m, tea.Batch(m.spin.Tick, run(c, args))
		}
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	case toolMsg:
		m.add(entry{kind: toolEntry, text: string(msg)})
		return m, nil
	case resultMsg:
		m.busy = false
		var tooMany *review.TooManyFilesError
		switch {
		case errors.As(msg.err, &tooMany):
			// Not a failure: the user decides whether to raise the limit.
			m.add(entry{kind: errEntry, label: "warning", text: msg.err.Error()})
		case msg.err != nil:
			m.add(entry{kind: errEntry, text: msg.err.Error()})
		default:
			m.add(entry{kind: replyEntry, label: msg.label, text: msg.text})
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
	status := dimStyle.Render("enter: send · tab: complete command · pgup/pgdn or mouse: scroll · esc: quit")
	if m.busy {
		status = m.spin.View() + " working..."
	}
	return m.view.View() + "\n" + status + "\n" + m.input.View()
}

func (m *model) add(e entry) {
	m.log = append(m.log, e)
	m.refresh()
}

func (m *model) refresh() {
	m.view.SetContent(render(m.log, m.width))
	m.view.GotoBottom()
}

// render lays out the scrollback: each exchange starts with a rule, questions and results get a
// labelled block with a colored left bar, tool calls and progress are dim lines in between.
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
		case replyEntry:
			color, ok := labelColors[e.label]
			if !ok {
				color = replyColor
			}
			b.WriteString(block(e.label, e.text, color, width))
		case errEntry:
			label, color := "error", errColor
			if e.label != "" {
				label, color = e.label, labelColors[e.label]
			}
			b.WriteString(block(label, e.text, color, width))
		}
	}
	return b.String()
}

func block(label, text string, color lipgloss.Color, width int) string {
	head := lipgloss.NewStyle().Bold(true).Foreground(color).Render(label)
	// Width covers content + padding; the 1-column bar is added outside it.
	return barStyle.BorderForeground(color).Width(width - 1).Render(head + "\n" + text)
}

func run(c commands.Command, args []string) tea.Cmd {
	return func() tea.Msg {
		text, err := c.Run(context.Background(), args)
		return resultMsg{label: c.Label, text: text, err: err}
	}
}

func formatCall(name string, args map[string]any) string {
	b, _ := json.Marshal(args)
	if s := string(b); s != "{}" && s != "null" {
		return name + " " + s
	}
	return name
}

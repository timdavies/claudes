package picker

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/timdavies/claudes/internal/session"
)

// ErrCancelled is returned when the user dismisses the picker.
var ErrCancelled = errors.New("picker: cancelled")

// ErrNoTTY is returned when there's no interactive terminal available.
var ErrNoTTY = errors.New("no interactive terminal: pass a session name explicitly")

// Disabled returns true if the picker should be skipped.
func Disabled() bool {
	if os.Getenv("CLAUDES_NO_INTERACTIVE") != "" {
		return true
	}
	return !term.IsTerminal(int(os.Stdin.Fd()))
}

type model struct {
	sessions []session.Session
	cursor   int
	chosen   *session.Session
	quit     bool
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "q", "esc":
			m.quit = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case "enter":
			s := m.sessions[m.cursor]
			m.chosen = &s
			return m, tea.Quit
		}
	}
	return m, nil
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
)

func (m model) View() string {
	if m.quit && m.chosen == nil {
		return ""
	}
	header := headerStyle.Render(fmt.Sprintf("? Select a session: (%d)", len(m.sessions)))
	out := header + "\n"
	for i, s := range m.sessions {
		marker := "   "
		row := fmt.Sprintf("%-14s %-8s %-8s %-22s %s",
			truncate(s.Name, 14), truncate(orDash(s.Project), 8), truncate(orDash(s.Model), 8),
			truncate(s.Dir, 22), s.Status)
		if i == m.cursor {
			marker = " ▸ "
			row = cursorStyle.Render(row)
		}
		out += marker + row + "\n"
	}
	out += "\n(↑/↓ to move, enter to select, q/esc to cancel)\n"
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// Pick prompts for a session. Returns ErrCancelled if the user backs out,
// ErrNoTTY if running non-interactively.
func Pick(sessions []session.Session) (session.Session, error) {
	if Disabled() {
		return session.Session{}, ErrNoTTY
	}
	if len(sessions) == 0 {
		return session.Session{}, errors.New("no sessions")
	}
	p := tea.NewProgram(model{sessions: sessions}, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return session.Session{}, err
	}
	m := final.(model)
	if m.chosen == nil {
		return session.Session{}, ErrCancelled
	}
	return *m.chosen, nil
}

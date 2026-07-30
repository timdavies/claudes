package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

type archivedMsg []archivedEntry

// archActionMsg reports the result of an archive/unarchive action so the list
// can refresh and surface a status line.
type archActionMsg struct {
	status string
	err    error
}

// loadArchivedCmd refreshes the archived-agents list off the event loop.
func loadArchivedCmd() tea.Cmd {
	return func() tea.Msg {
		reg, err := pinnedRegistry()
		if err != nil {
			return archivedMsg(nil)
		}
		return archivedMsg(archivedEntries(reg))
	}
}

func (m tuiModel) currentArchived() (archivedEntry, bool) {
	if m.archCursor < 0 || m.archCursor >= len(m.archived) {
		return archivedEntry{}, false
	}
	return m.archived[m.archCursor], true
}

// updateArchivedList handles keys while the cursor is in the archived region.
func (m tuiModel) updateArchivedList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	folded := m.isFolded(foldKeyArchived)
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case " ", "space", "z":
		(&m).toggleFoldCurrent()
	case "up", "k":
		if !folded && m.archCursor > 0 {
			m.archCursor--
		} else if len(m.schedules) > 0 {
			m.region = regionSchedules
			m.schedCursor = len(m.schedules) - 1
		} else if len(m.rows) > 0 {
			m.region = regionAgents
			m.cursor = len(m.rows) - 1
			m.col = 0
			(&m).snapCursorVisible(-1)
		}
	case "down", "j":
		if !folded && m.archCursor < len(m.archived)-1 {
			m.archCursor++
		}
	case "g", "home":
		m.archCursor = 0
	case "G", "end":
		m.archCursor = max(0, len(m.archived)-1)
	// enter or U restores; both are non-destructive so a bare Enter is fine.
	case "enter", "U":
		if ae, ok := m.currentArchived(); ok {
			m.status = "restoring " + ae.name + "…"
			return m, unarchiveSelectedCmd(m.client, m.cfg, ae.name)
		}
	case "r":
		m.status = "refreshed"
		return m, loadArchivedCmd()
	}
	return m, nil
}

// archiveSelectedCmd archives the named agent off the event loop.
func archiveSelectedCmd(client *tmux.Client, cfg *config.Config, target session.Session) tea.Cmd {
	return func() tea.Msg {
		if err := archiveSession(client, cfg, &target); err != nil {
			return archActionMsg{err: err}
		}
		return archActionMsg{status: "archived " + target.Name}
	}
}

func unarchiveSelectedCmd(client *tmux.Client, cfg *config.Config, name string) tea.Cmd {
	return func() tea.Msg {
		reg, err := pinnedRegistry()
		if err != nil {
			return archActionMsg{err: err}
		}
		if err := unarchiveSession(client, cfg, reg, name); err != nil {
			return archActionMsg{err: err}
		}
		return archActionMsg{status: "restored " + name}
	}
}

// renderArchived renders the "Archived (N)" section. cursor is the selected
// index, or -1 when the region is inactive. Returns "" when there are none.
func renderArchived(entries []archivedEntry, cursor, width int, folded bool) string {
	if len(entries) == 0 {
		return ""
	}
	if folded {
		label := fmt.Sprintf("▸ Archived (%d)", len(entries))
		if cursor >= 0 {
			return cardSelected.Render(label)
		}
		return cardGroup.Render(label)
	}
	header := cardGroup.Render(fmt.Sprintf("▾ Archived (%d)", len(entries)))
	lines := []string{header}
	for i, ae := range entries {
		label := ae.name
		meta := ""
		if ae.entry.Group != "" {
			meta += "  " + ae.entry.Group
		}
		if ae.entry.PR != "" {
			meta += "  " + ae.entry.PR
		}
		row := cardNamePaused.Render(label) + cardMeta.Render(meta)
		if i == cursor {
			row = cardSelected.Render(" "+label+" ") + cardMeta.Render(meta)
		}
		lines = append(lines, "  "+row)
	}
	out := ""
	for i, ln := range lines {
		if i > 0 {
			out += "\n"
		}
		out += truncateLine(ln, width)
	}
	return out
}

// truncateLine trims a rendered line to width columns (best-effort; keeps ANSI
// intact for the common short case where no trim is needed).
func truncateLine(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

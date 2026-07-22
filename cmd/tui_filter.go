package cmd

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// rowMatches reports whether a row satisfies the active filter (case-insensitive
// substring over name or group). An empty filter matches everything.
func (m tuiModel) rowMatches(r agentRow) bool {
	if m.filter == "" {
		return true
	}
	q := strings.ToLower(m.filter)
	return strings.Contains(strings.ToLower(r.Name), q) ||
		strings.Contains(strings.ToLower(r.Group), q)
}

// filteredRows returns the rows visible under the current filter, along with the
// index (into that slice) of the row m.cursor points at, or -1 if the cursor's
// row is filtered out. The cursor itself always indexes the full m.rows.
func (m tuiModel) filteredRows() (rows []agentRow, cursorIdx int) {
	cursorIdx = -1
	for i, r := range m.rows {
		if !m.rowMatches(r) {
			continue
		}
		if i == m.cursor {
			cursorIdx = len(rows)
		}
		rows = append(rows, r)
	}
	return rows, cursorIdx
}

// snapCursorToMatch moves the cursor onto the nearest matching row (searching
// forward from its current spot, then backward) so it never rests on a filtered
// row. No-op when nothing matches.
func (m *tuiModel) snapCursorToMatch() {
	if len(m.rows) == 0 || m.rowMatches(m.rows[m.cursor]) {
		return
	}
	for i := m.cursor + 1; i < len(m.rows); i++ {
		if m.rowMatches(m.rows[i]) {
			m.cursor = i
			return
		}
	}
	for i := m.cursor - 1; i >= 0; i-- {
		if m.rowMatches(m.rows[i]) {
			m.cursor = i
			return
		}
	}
}

// moveMatch moves the cursor to the next (dir +1) or previous (dir -1) matching
// row, staying put at the ends. Used for up/down while filtering.
func (m *tuiModel) moveMatch(dir int) {
	for i := m.cursor + dir; i >= 0 && i < len(m.rows); i += dir {
		if m.rowMatches(m.rows[i]) {
			m.cursor = i
			m.col = 0
			return
		}
	}
}

// updateFilterKey handles a keystroke while the filter input is active. Returns
// handled=false for keys the filter doesn't consume (so the caller runs its
// normal list handling — navigation, enter, etc.).
func (m tuiModel) updateFilterKey(msg tea.KeyMsg) (tuiModel, tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter = ""
		m.status = ""
		return m, nil, true
	case "enter":
		// Accept the highlighted match and open it; leave filter mode and clear
		// the query so the next '/' starts fresh instead of prefilled.
		m.filtering = false
		m.filter = ""
		md, cmd := m.activate()
		if mm, ok := md.(tuiModel); ok {
			return mm, cmd, true
		}
		return m, cmd, true
	case "backspace":
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
		}
		(&m).snapCursorToMatch()
		return m, nil, true
	case "up", "down":
		return m, nil, false // arrows move between matches; j/k are query text
	default:
		// Printable runes extend the query (a single keystroke is one rune; a
		// pasted chunk may carry several).
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			m.filter += string(msg.Runes)
			(&m).snapCursorToMatch()
			return m, nil, true
		}
	}
	return m, nil, false
}

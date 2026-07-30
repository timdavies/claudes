package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/timdavies/claudes/internal/daemon"
)

// Fold state: which TUI sections are collapsed. A collapsed section shows only
// its header (with a ▸ caret and a count); its rows are hidden and skipped by
// navigation. The set persists across TUI restarts in a small JSON file next to
// the other cache state, so a folded schedules section stays folded.

const (
	foldKeySchedules = "\x00schedules" // NUL-prefixed so it can't collide with a group name
	foldKeyArchived  = "\x00archived"
)

// foldKeyGroup namespaces an agent group's fold key. Groups are folded by name.
func foldKeyGroup(group string) string { return "group:" + group }

func foldStatePath() (string, error) {
	dir, err := daemon.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tui_folds.json"), nil
}

// loadFolds reads the persisted fold set. A missing or unreadable file yields an
// empty (nothing folded) set — fold state is cosmetic, never worth failing over.
func loadFolds() map[string]bool {
	folds := map[string]bool{}
	path, err := foldStatePath()
	if err != nil {
		return folds
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return folds
	}
	var keys []string
	if json.Unmarshal(data, &keys) != nil {
		return folds
	}
	for _, k := range keys {
		folds[k] = true
	}
	return folds
}

// saveFolds atomically writes the folded keys. Best-effort: a write failure just
// means the fold state won't survive this restart, which is harmless.
func saveFolds(folds map[string]bool) {
	path, err := foldStatePath()
	if err != nil {
		return
	}
	var keys []string
	for k, on := range folds {
		if on {
			keys = append(keys, k)
		}
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// moveCursor steps the agent cursor by dir, skipping rows hidden inside folded
// groups. Returns false when it hits the top/bottom edge without moving (the
// caller uses that to hand off to an adjacent region).
func (m *tuiModel) moveCursor(dir int) bool {
	i := m.cursor
	for {
		i += dir
		if i < 0 || i >= len(m.rows) {
			return false
		}
		if !m.isRowHidden(i) {
			m.cursor = i
			m.col = 0
			return true
		}
	}
}

// snapCursorVisible pulls the cursor onto the nearest non-hidden row after a
// jump (pgup/pgdn/home/end may land inside a folded group). It searches in
// preferDir first, then the other way.
func (m *tuiModel) snapCursorVisible(preferDir int) {
	if m.cursor < 0 || m.cursor >= len(m.rows) || !m.isRowHidden(m.cursor) {
		return
	}
	for _, dir := range []int{preferDir, -preferDir} {
		i := m.cursor
		for i >= 0 && i < len(m.rows) {
			if !m.isRowHidden(i) {
				m.cursor = i
				return
			}
			i += dir
		}
	}
}

func (m *tuiModel) isFolded(key string) bool { return m.folds != nil && m.folds[key] }

func (m *tuiModel) groupFolded(group string) bool {
	return group != "" && m.isFolded(foldKeyGroup(group))
}

// isRowHidden reports whether agent row i is collapsed out of view: it's in a
// folded group and isn't that group's first (representative) row, which stays
// visible as the fold header. Folds are ignored while filtering.
func (m *tuiModel) isRowHidden(i int) bool {
	if m.filtering || i < 0 || i >= len(m.rows) {
		return false
	}
	r := m.rows[i]
	if !m.groupFolded(r.Group) {
		return false
	}
	return i > 0 && m.rows[i-1].Group == r.Group // not the first row of the group
}

// toggleFoldCurrent flips the fold state of the section the cursor is in, then
// persists. For a folded agent group it snaps the cursor to the group's
// representative row so it never strands on a now-hidden row.
func (m *tuiModel) toggleFoldCurrent() {
	if m.folds == nil {
		m.folds = map[string]bool{}
	}
	var key string
	switch m.region {
	case regionSchedules:
		key = foldKeySchedules
	case regionArchived:
		key = foldKeyArchived
	default:
		if m.cursor < 0 || m.cursor >= len(m.rows) {
			return
		}
		group := m.rows[m.cursor].Group
		if group == "" {
			m.status = "ungrouped agents can't be folded"
			return
		}
		key = foldKeyGroup(group)
		if !m.folds[key] { // about to fold — park cursor on the representative row
			for i := range m.rows {
				if m.rows[i].Group == group {
					m.cursor = i
					m.col = 0
					break
				}
			}
		}
	}
	m.folds[key] = !m.folds[key]
	if !m.folds[key] {
		delete(m.folds, key)
	}
	saveFolds(m.folds)
}

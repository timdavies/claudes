package cmd

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/schedule"
	"github.com/timdavies/claudes/internal/session"
)

func foldModel() tuiModel {
	return tuiModel{
		cfg: &config.Config{Prefix: "claudes-", Projects: map[string]config.Project{}},
		rows: []agentRow{
			{Name: "solo", Status: session.StatusIdle}, // default group
			{Name: "rev-1", Group: "review", Status: session.StatusIdle},
			{Name: "rev-2", Group: "review", Status: session.StatusIdle},
			{Name: "rev-3", Group: "review", Status: session.StatusIdle},
		},
		folds: map[string]bool{},
	}
}

func TestFoldGroupHidesRowsAndSnapsCursor(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // keep saveFolds off the real cache
	m := foldModel()

	// Cursor onto a non-first row of the review group, then fold with space.
	m.cursor = 2
	m, _ = send(m, key(" "))

	if !m.groupFolded("review") {
		t.Fatal("space should fold the review group")
	}
	// Folding parks the cursor on the group's representative (first) row.
	if m.cursor != 1 {
		t.Fatalf("cursor should snap to the representative row (1), got %d", m.cursor)
	}
	// Representative visible; the rest of the group hidden.
	if m.isRowHidden(1) {
		t.Error("representative row must stay visible")
	}
	if !m.isRowHidden(2) || !m.isRowHidden(3) {
		t.Error("non-representative rows of a folded group must be hidden")
	}
	// The default-group row is never affected.
	if m.isRowHidden(0) {
		t.Error("ungrouped row must never be hidden")
	}

	// down from the representative skips the hidden rows entirely (no schedules
	// below, so it stays put on the last visible agent row).
	before := m.cursor
	m, _ = send(m, key("j"))
	if m.cursor != before {
		t.Fatalf("down should skip hidden rows, cursor moved %d->%d", before, m.cursor)
	}

	// The scroll geometry drops the hidden rows' lines.
	foldedLines, _, _ := m.bodyGeometry()
	m2 := foldModel()
	expandedLines, _, _ := m2.bodyGeometry()
	if len(foldedLines) >= len(expandedLines) {
		t.Fatalf("folded body (%d lines) should be shorter than expanded (%d)", len(foldedLines), len(expandedLines))
	}

	// Unfold with space again.
	m, _ = send(m, key(" "))
	if m.groupFolded("review") {
		t.Fatal("second space should unfold")
	}
	if m.isRowHidden(2) {
		t.Error("rows visible again after unfold")
	}
}

func TestFoldPersistsAcrossReload(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := foldModel()
	m.cursor = 1
	m, _ = send(m, key(" ")) // fold review

	reloaded := loadFolds()
	if !reloaded[foldKeyGroup("review")] {
		t.Fatalf("fold state should persist, got %v", reloaded)
	}

	// Unfold and confirm the key is dropped from the persisted set.
	m, _ = send(m, key(" "))
	if loadFolds()[foldKeyGroup("review")] {
		t.Fatal("unfolding should clear the persisted key")
	}
}

func TestFoldSchedulesCollapsesSection(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := foldModel()
	m.schedules = []schedule.Schedule{
		{ID: "1", Name: "nightly", Enabled: true},
		{ID: "2", Name: "hourly", Enabled: true},
	}
	m.region = regionSchedules

	m, _ = send(m, key(" "))
	if !m.isFolded(foldKeySchedules) {
		t.Fatal("space should fold the schedules section")
	}

	// Collapsed render is a single caret+count line, not one line per schedule.
	out := renderSchedules(m.schedules, nil, nil, 0, 200, true)
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
		t.Fatalf("folded schedules should be one line, got %q", out)
	}
	if !strings.Contains(out, "▸") || !strings.Contains(out, "(2)") {
		t.Fatalf("folded header should show caret + count, got %q", out)
	}
}

func TestEnterExpandsFoldedGroup(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := foldModel()
	m.cursor = 1
	m, _ = send(m, key(" ")) // fold review; cursor on the representative row

	// Enter on the collapsed header expands it rather than attaching to the agent.
	m, _ = send(m, special(tea.KeyEnter))
	if m.groupFolded("review") {
		t.Fatal("enter on a folded header should expand the group")
	}
	if m.exitAction != exitNone {
		t.Fatal("enter should not trigger attach while expanding a fold")
	}
}

func TestUngroupedCannotFold(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := foldModel()
	m.cursor = 0 // the default-group row
	m, _ = send(m, key(" "))
	if len(m.folds) != 0 {
		t.Fatalf("ungrouped agents can't fold, got folds %v", m.folds)
	}
	if !strings.Contains(m.status, "can't be folded") {
		t.Errorf("expected a status hint, got %q", m.status)
	}
}

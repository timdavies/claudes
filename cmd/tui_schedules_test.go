package cmd

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/timdavies/claudes/internal/schedule"
	"github.com/timdavies/claudes/internal/session"
)

func schedModel() tuiModel {
	m := baseModel()
	m.rows = []agentRow{{Name: "alpha", Status: session.StatusIdle, Dir: "/tmp/alpha"}}
	m.schedules = []schedule.Schedule{
		{ID: "1", Name: "nightly", Kind: schedule.KindDaily, AtClock: "09:00", Enabled: true},
	}
	m.width = 80
	return m
}

func TestRegionNavigation(t *testing.T) {
	m := schedModel()
	// Cursor starts in the agents region on the last (only) row.
	if m.region != regionAgents {
		t.Fatal("should start in agents region")
	}
	// Down from the last agent crosses into the schedules region.
	m, _ = send(m, key("j"))
	if m.region != regionSchedules || m.schedCursor != 0 {
		t.Fatalf("down should enter schedules region; got region=%d cursor=%d", m.region, m.schedCursor)
	}
	// Up from the first schedule returns to the agents region.
	m, _ = send(m, key("k"))
	if m.region != regionAgents {
		t.Fatal("up should return to agents region")
	}
}

func TestScheduleKeysGatedByRegion(t *testing.T) {
	m := schedModel()
	m.region = regionSchedules
	// `N` in the schedules region opens the schedule form, not the agent form.
	m, _ = send(m, key("N"))
	if m.mode != modeScheduleForm || m.schedForm == nil {
		t.Fatalf("N should open schedule form; mode=%d", m.mode)
	}
	// esc returns to the list.
	m, _ = send(m, special(tea.KeyEsc))
	if m.mode != modeList {
		t.Fatal("esc should return to list")
	}
}

func TestScheduleDeleteConfirm(t *testing.T) {
	m := schedModel()
	m.region = regionSchedules
	m, _ = send(m, key("X"))
	if m.mode != modeScheduleConfirmRm || m.schedToRm.ID != "1" {
		t.Fatalf("X should arm delete confirm; mode=%d", m.mode)
	}
	m, _ = send(m, key("n"))
	if m.mode != modeList {
		t.Fatal("n should cancel delete")
	}
}

func TestScheduleSectionRenders(t *testing.T) {
	m := schedModel()
	m.height = 24
	out := m.View()
	if !strings.Contains(out, "schedules") || !strings.Contains(out, "nightly") {
		t.Fatalf("view should show the schedules section:\n%s", out)
	}
}

func TestRenderSchedulesTruncationIsAnsiSafe(t *testing.T) {
	scheds := []schedule.Schedule{
		{ID: "1", Name: "a-really-quite-long-schedule-name", Kind: schedule.KindInterval, EverySec: 300, Enabled: true},
		{ID: "2", Name: "short", Kind: schedule.KindInterval, EverySec: 600, Enabled: false},
	}
	const width = 30
	out := renderSchedules(scheds, map[string]string{"1": "ok 2m ago"}, nil, 0, width)

	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line exceeds width %d (got %d): %q", width, w, line)
		}
	}
	// The selected row (cursor 0) must carry the bright chip; an unselected one
	// must not — this is what the old rune-truncate corrupted.
	sel := cardSelected.Render("a-really-quite-long-schedule-name")
	chipStart := sel[:strings.Index(sel, "a")]
	if !strings.Contains(out, chipStart) {
		t.Fatalf("selected schedule missing the chip styling:\n%q", out)
	}
}

func TestRenderSchedulesShowsCumulativeCost(t *testing.T) {
	scheds := []schedule.Schedule{
		{ID: "16", Name: "deprecation-watch", Kind: schedule.KindDaily, AtClock: "09:00", Enabled: true},
		{ID: "99", Name: "never-run", Kind: schedule.KindDaily, AtClock: "09:00", Enabled: true},
	}
	out := renderSchedules(scheds, nil, map[string]float64{"16": 3.59}, -1, 200)
	if !strings.Contains(out, "$3.59") {
		t.Fatalf("expected cumulative $3.59 on the deprecation-watch row:\n%s", out)
	}
	// Every row shows a $ amount for column alignment — a no-spend task is $0.00.
	if !strings.Contains(out, "$0.00") {
		t.Fatalf("no-spend task should render $0.00 (column alignment):\n%s", out)
	}
}

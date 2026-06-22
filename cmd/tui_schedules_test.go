package cmd

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	// `n` in the schedules region opens the schedule form, not the agent form.
	m, _ = send(m, key("n"))
	if m.mode != modeScheduleForm || m.schedForm == nil {
		t.Fatalf("n should open schedule form; mode=%d", m.mode)
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
	m, _ = send(m, key("x"))
	if m.mode != modeScheduleConfirmRm || m.schedToRm.ID != "1" {
		t.Fatalf("x should arm delete confirm; mode=%d", m.mode)
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

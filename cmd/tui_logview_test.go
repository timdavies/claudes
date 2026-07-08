package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/timdavies/claudes/internal/schedule"
)

func TestRunListWindowsToHeight(t *testing.T) {
	runs := make([]schedule.Run, 200)
	for i := range runs {
		runs[i] = schedule.Run{Status: "done", StartedAt: "2026-07-08T11:00:00Z"}
	}
	v := &schedLogView{schedule: schedule.Schedule{Name: "big"}, runs: runs, sel: 199}
	out := v.view(120, 24)
	if lines := strings.Count(out, "\n") + 1; lines > 24 {
		t.Fatalf("run list overflows terminal: %d lines > 24", lines)
	}
	// The selected (last) run must be within the window, and an "above"
	// indicator present since we're at the bottom of 200.
	if !strings.Contains(out, "more above") {
		t.Errorf("expected a 'more above' indicator when selection is at the tail")
	}
}

func TestRunOutputTailsByDefault(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "line-%d\n", i)
	}
	sb.WriteString("NEWEST-RESULT-LINE\n")
	// scroll = 1<<30 is what the enter handler sets; view() clamps it to the
	// bottom, so the newest line is visible.
	v := &schedLogView{
		schedule: schedule.Schedule{Name: "b"},
		runs:     []schedule.Run{{Status: "done"}},
		body:     sb.String(),
		scroll:   1 << 30,
	}
	out := v.view(120, 24)
	if !strings.Contains(out, "NEWEST-RESULT-LINE") {
		t.Fatalf("run output should tail to the newest line by default")
	}
	if lines := strings.Count(out, "\n") + 1; lines > 24 {
		t.Fatalf("body view overflows: %d lines > 24", lines)
	}
	// The oldest line must NOT be on screen when tailing.
	if strings.Contains(out, "line-0\n") {
		t.Errorf("tail view should not show the very first line")
	}
}

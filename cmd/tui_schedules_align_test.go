package cmd

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/timdavies/claudes/internal/schedule"
)

func TestScheduleColumnsAlign(t *testing.T) {
	scs := []schedule.Schedule{
		{ID: "1", Name: "transcript-grab", Kind: schedule.KindInterval, EverySec: 3600, StartHour: 10, EndHour: 17, Days: []int{1, 2, 3, 4, 5}, Enabled: true},
		{ID: "2", Name: "pr-owl", Kind: schedule.KindInterval, EverySec: 900, StartHour: 7, EndHour: 19, Days: []int{1, 2, 3, 4, 5}, Enabled: true},
		{ID: "3", Name: "brag-doc", Kind: schedule.KindDaily, AtClock: "17:00", Enabled: true},
		{ID: "4", Name: "deps-bump", Kind: schedule.KindDaily, AtClock: "08:30", Enabled: false},
	}
	last := map[string]string{"1": "done 15m ago", "3": "done 21h ago"}
	cost := map[string]float64{"1": 78.29, "3": 12.81, "4": 3.64}

	out := renderSchedules(scs, last, cost, -1, 200, false)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 5 { // header + 4 rows
		t.Fatalf("expected header + 4 rows, got %d lines", len(lines))
	}

	// Each data row: dot is the first non-space glyph; the cadence lane starts
	// at the same column on every row.
	cadenceCol := -1
	for _, ln := range lines[1:] {
		plain := ansi.Strip(ln)
		trimmed := strings.TrimLeft(plain, " ")
		if r := []rune(trimmed)[0]; r != '●' && r != '○' {
			t.Errorf("row does not lead with a status dot: %q", plain)
		}
		// Column where the cadence text ("every"/"daily") begins.
		col := strings.Index(plain, "every")
		if col < 0 {
			col = strings.Index(plain, "daily")
		}
		if cadenceCol == -1 {
			cadenceCol = col
		} else if col != cadenceCol {
			t.Errorf("cadence lane misaligned: got col %d, want %d (%q)", col, cadenceCol, plain)
		}
	}
}

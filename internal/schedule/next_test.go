package schedule

import (
	"testing"
	"time"
)

// at builds a local time on a fixed day for deterministic tests.
func at(hour, min int) time.Time {
	return time.Date(2026, 6, 17, hour, min, 0, 0, time.Local)
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func TestDueInterval(t *testing.T) {
	now := at(12, 0)
	base := Schedule{Kind: KindInterval, Enabled: true, EverySec: 300, CreatedAt: rfc(at(0, 0))}

	// Last fired 10m ago → overdue → due.
	sc := base
	sc.LastFired = rfc(at(11, 50))
	if !Due(sc, now) {
		t.Fatal("interval 10m overdue should be due")
	}

	// Last fired 1m ago → not yet.
	sc.LastFired = rfc(at(11, 59))
	if Due(sc, now) {
		t.Fatal("interval fired 1m ago should not be due")
	}

	// Disabled → never due.
	sc.Enabled = false
	sc.LastFired = rfc(at(11, 50))
	if Due(sc, now) {
		t.Fatal("disabled schedule should not be due")
	}
}

func TestIntervalWindow(t *testing.T) {
	// Overdue but outside the 9–18 window → not due, and next fire snaps to 9am.
	night := at(23, 0)
	sc := Schedule{Kind: KindInterval, Enabled: true, EverySec: 300,
		StartHour: 9, EndHour: 18, LastFired: rfc(at(22, 50))}
	if Due(sc, night) {
		t.Fatal("interval outside window should not be due")
	}
	next, ok := NextFire(sc, night)
	if !ok {
		t.Fatal("expected a next fire")
	}
	if next.Hour() != 9 || next.Day() != 18 {
		t.Fatalf("expected next fire tomorrow 09:00, got %v", next)
	}
}

func TestDueDaily(t *testing.T) {
	// 09:00 daily, now 10:00, not fired today → overdue → due.
	sc := Schedule{Kind: KindDaily, Enabled: true, AtClock: "09:00"}
	if !Due(sc, at(10, 0)) {
		t.Fatal("daily past its time, not fired, should be due")
	}
	// Future time today → not due.
	sc.AtClock = "11:00"
	if Due(sc, at(10, 0)) {
		t.Fatal("daily before its time should not be due")
	}
	// Already fired today → not due, next is tomorrow.
	sc.AtClock = "09:00"
	sc.LastFired = rfc(at(9, 0))
	if Due(sc, at(10, 0)) {
		t.Fatal("daily already fired today should not be due")
	}
	next, _ := NextFire(sc, at(10, 0))
	if next.Day() != 18 || next.Hour() != 9 {
		t.Fatalf("expected tomorrow 09:00, got %v", next)
	}
}

func TestDueOnce(t *testing.T) {
	sc := Schedule{Kind: KindOnce, Enabled: true, AtTime: rfc(at(8, 0))}
	if !Due(sc, at(10, 0)) {
		t.Fatal("once in the past should be due")
	}
	// Future → not due.
	sc.AtTime = rfc(at(14, 0))
	if Due(sc, at(10, 0)) {
		t.Fatal("once in the future should not be due")
	}
	// Fired → never again.
	sc.AtTime = rfc(at(8, 0))
	sc.LastFired = rfc(at(8, 0))
	if _, ok := NextFire(sc, at(10, 0)); ok {
		t.Fatal("once already fired should have no next fire")
	}
}

func TestParseEvery(t *testing.T) {
	if sec, err := ParseEvery("5m"); err != nil || sec != 300 {
		t.Fatalf("5m => %d, %v", sec, err)
	}
	if _, err := ParseEvery("nonsense"); err == nil {
		t.Fatal("expected error for bad interval")
	}
}

func TestParseDays(t *testing.T) {
	cases := map[string][]int{
		"":            nil,
		"mon":         {1},
		"mon,thu":     {1, 4},
		"thu,mon":     {1, 4}, // sorted
		"Mon, Monday": {1},    // case-insensitive, 3-letter fold, deduped
		"sun,sat":     {0, 6},
	}
	for in, want := range cases {
		got, err := ParseDays(in)
		if err != nil {
			t.Fatalf("ParseDays(%q) error: %v", in, err)
		}
		if len(got) != len(want) {
			t.Fatalf("ParseDays(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("ParseDays(%q) = %v, want %v", in, got, want)
			}
		}
	}
	if _, err := ParseDays("funday"); err == nil {
		t.Fatal("expected error for invalid day")
	}
}

func TestSpecDailyWithDays(t *testing.T) {
	weekly := Schedule{Kind: KindDaily, AtClock: "09:00", Days: []int{1}}
	if got := Spec(weekly); got != "mon 09:00" {
		t.Fatalf("Spec weekly = %q, want 'mon 09:00'", got)
	}
	plain := Schedule{Kind: KindDaily, AtClock: "09:00"}
	if got := Spec(plain); got != "daily 09:00" {
		t.Fatalf("Spec plain daily = %q, want 'daily 09:00'", got)
	}
}

func TestDailyDaysOnlyFiresOnAllowedDay(t *testing.T) {
	// A Monday-only daily at 09:00.
	sc := Schedule{Kind: KindDaily, AtClock: "09:00", Days: []int{1}, Enabled: true}

	mon := time.Date(2026, 6, 29, 9, 0, 0, 0, time.Local) // 2026-06-29 is a Monday
	if mon.Weekday() != time.Monday {
		t.Fatalf("test fixture wrong: %v is not Monday", mon)
	}
	if !Due(sc, mon) {
		t.Fatal("Monday-only daily should be Due on Monday at 09:00")
	}

	tue := time.Date(2026, 6, 30, 9, 0, 0, 0, time.Local) // Tuesday
	if Due(sc, tue) {
		t.Fatal("Monday-only daily must NOT be Due on Tuesday (no wasted fire)")
	}
	// Next fire from Tuesday lands on the following Monday at 09:00.
	next, ok := NextFire(sc, tue)
	if !ok || next.Weekday() != time.Monday || next.Hour() != 9 {
		t.Fatalf("next fire from Tuesday = %v (ok=%v), want next Monday 09:00", next, ok)
	}
}

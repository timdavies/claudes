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

package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NextFire returns the next local time at/after now that sc should fire, and
// ok=false when it never will again (disabled, or a `once` already fired).
//
//   - once: AtTime; ok=false once fired. An overdue AtTime returns now.
//   - interval: LastFired (else CreatedAt) + EverySec, snapped forward into the
//     active window [StartHour,EndHour).
//   - daily: today at AtClock, or tomorrow if that's already past / already
//     fired today. The window doesn't apply — AtClock is the intended time.
func NextFire(sc Schedule, now time.Time) (time.Time, bool) {
	if !sc.Enabled {
		return time.Time{}, false
	}
	switch sc.Kind {
	case KindOnce:
		if sc.LastFired != "" {
			return time.Time{}, false
		}
		at, err := parseOnce(sc.AtTime)
		if err != nil {
			return time.Time{}, false
		}
		if at.Before(now) {
			return now, true
		}
		return at, true

	case KindInterval:
		if sc.EverySec <= 0 {
			return time.Time{}, false
		}
		base := now
		if sc.LastFired != "" {
			if t, err := time.Parse(time.RFC3339, sc.LastFired); err == nil {
				base = t.Local()
			}
		} else if sc.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, sc.CreatedAt); err == nil {
				base = t.Local()
			}
		}
		next := base.Add(time.Duration(sc.EverySec) * time.Second)
		if next.Before(now) {
			next = now
		}
		return snapIntoWindow(next, sc.StartHour, sc.EndHour), true

	case KindDaily:
		hh, mm, err := parseClock(sc.AtClock)
		if err != nil {
			return time.Time{}, false
		}
		cand := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		// Once today's run is recorded, the next fire is tomorrow. Otherwise
		// today's slot stands — even if it's already past, so Due() (next<=now)
		// catches an overdue daily instead of skipping it to tomorrow.
		if firedToday(sc, now) {
			cand = cand.AddDate(0, 0, 1)
		}
		return cand, true
	}
	return time.Time{}, false
}

// Due reports whether sc should fire at now.
func Due(sc Schedule, now time.Time) bool {
	next, ok := NextFire(sc, now)
	if !ok {
		return false
	}
	if next.After(now) {
		return false
	}
	// Interval respects the active window; once/daily fire at their explicit time.
	if sc.Kind == KindInterval && !inWindow(now, sc.StartHour, sc.EndHour) {
		return false
	}
	return true
}

// snapIntoWindow advances t to the next StartHour boundary if its hour falls
// outside [start,end). A full-day window (start==end) is treated as no window.
func snapIntoWindow(t time.Time, start, end int) time.Time {
	if start == end {
		return t
	}
	if inWindow(t, start, end) {
		return t
	}
	day := t
	if t.Hour() >= end {
		day = t.AddDate(0, 0, 1)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), start, 0, 0, 0, t.Location())
}

func inWindow(t time.Time, start, end int) bool {
	if start == end {
		return true
	}
	h := t.Hour()
	return h >= start && h < end
}

func firedToday(sc Schedule, now time.Time) bool {
	if sc.LastFired == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, sc.LastFired)
	if err != nil {
		return false
	}
	t = t.Local()
	return t.Year() == now.Year() && t.YearDay() == now.YearDay()
}

func parseOnce(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local(), nil
	}
	// Accept a friendlier "2006-01-02 15:04" in local time too.
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid datetime %q", s)
}

// parseClock parses "HH:MM" 24h.
func parseClock(s string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
	}
	return hh, mm, nil
}

// ParseEvery turns "5m"/"30s"/"2h" into seconds.
func ParseEvery(s string) (int, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid interval %q (e.g. 5m, 2h)", s)
	}
	if d < time.Second {
		return 0, fmt.Errorf("interval must be at least 1s")
	}
	return int(d.Seconds()), nil
}

// ParseClock validates an "HH:MM" string and returns it normalized.
func ParseClock(s string) (string, error) {
	hh, mm, err := parseClock(s)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%02d:%02d", hh, mm), nil
}

// Spec renders a human-readable cadence for the TUI/CLI.
func Spec(sc Schedule) string {
	switch sc.Kind {
	case KindInterval:
		d := time.Duration(sc.EverySec) * time.Second
		w := ""
		if sc.StartHour != sc.EndHour {
			w = fmt.Sprintf(" · %d–%d", sc.StartHour, sc.EndHour)
		}
		return "every " + shortDur(d) + w
	case KindDaily:
		return "daily " + sc.AtClock
	case KindOnce:
		if t, err := parseOnce(sc.AtTime); err == nil {
			return "once " + t.Format("Jan 2 15:04")
		}
		return "once " + sc.AtTime
	}
	return string(sc.Kind)
}

func shortDur(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d.Minutes())) + "m"
	default:
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
}

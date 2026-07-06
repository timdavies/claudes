package schedule

import (
	"fmt"
	"sort"
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
		next = snapIntoWindow(next, sc.StartHour, sc.EndHour)
		// Honor a day-of-week restriction: skip forward to the next allowed
		// weekday (keeping the in-window time) so off days never fire.
		next = advanceToAllowedDay(next, sc.Days)
		return next, true

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
		// With a day-of-week filter, advance to the next allowed weekday (keeping
		// AtClock). A non-allowed today is pushed forward, so it never fires —
		// no wasted no-op runs on off days.
		cand = advanceToAllowedDay(cand, sc.Days)
		return cand, true
	}
	return time.Time{}, false
}

// advanceToAllowedDay bumps t forward by whole days until its weekday is in
// days. Empty days means every day is allowed (no-op).
func advanceToAllowedDay(t time.Time, days []int) time.Time {
	if len(days) == 0 {
		return t
	}
	for i := 0; i < 7; i++ {
		if dayAllowed(t, days) {
			return t
		}
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func dayAllowed(t time.Time, days []int) bool {
	if len(days) == 0 {
		return true
	}
	wd := int(t.Weekday())
	for _, d := range days {
		if d == wd {
			return true
		}
	}
	return false
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
	// Interval respects the active window AND any day-of-week restriction;
	// once/daily fire at their explicit time (daily's day filter is enforced
	// via NextFire). dayAllowed is true when Days is empty.
	if sc.Kind == KindInterval {
		if !inWindow(now, sc.StartHour, sc.EndHour) || !dayAllowed(now, sc.Days) {
			return false
		}
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

// dayNames maps a weekday int (0=Sun..6=Sat) to its short name.
var dayNames = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseDays turns "mon" or "mon,thu" (also full names / 3-letter, any case)
// into sorted, deduped weekday ints (0=Sun..6=Sat). Empty input → nil (every
// day).
func ParseDays(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	var out []int
	for _, tok := range strings.Split(s, ",") {
		name := strings.ToLower(strings.TrimSpace(tok))
		if name == "" {
			continue
		}
		if len(name) > 3 {
			name = name[:3]
		}
		wd := -1
		for i, n := range dayNames {
			if n == name {
				wd = i
				break
			}
		}
		if wd < 0 {
			return nil, fmt.Errorf("invalid day %q (use mon,tue,wed,thu,fri,sat,sun)", tok)
		}
		if !seen[wd] {
			seen[wd] = true
			out = append(out, wd)
		}
	}
	sort.Ints(out)
	return out, nil
}

// FormatDays renders weekday ints back to a compact label in week order:
// "weekdays" for Mon–Fri, "weekends" for Sat+Sun, else "mon,thu".
func FormatDays(days []int) string {
	sorted := append([]int(nil), days...)
	sort.Ints(sorted)
	names := make([]string, 0, len(sorted))
	for _, d := range sorted {
		if d >= 0 && d < 7 {
			names = append(names, dayNames[d])
		}
	}
	switch strings.Join(names, ",") {
	case "mon,tue,wed,thu,fri":
		return "weekdays"
	case "sun,sat":
		return "weekends"
	}
	return strings.Join(names, ",")
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
		days := ""
		if len(sc.Days) > 0 {
			days = " · " + FormatDays(sc.Days)
		}
		return "every " + shortDur(d) + w + days
	case KindDaily:
		if len(sc.Days) > 0 {
			return FormatDays(sc.Days) + " " + sc.AtClock
		}
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

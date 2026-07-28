package policy

import (
	"fmt"
	"time"
)

// LocalWindow is a daily wall-clock range in a fixed IANA timezone.
// Minutes are in [0, 1440). The active set is half-open [StartMinute, EndMinute)
// in local time. If StartMinute > EndMinute, the window crosses midnight.
//
// StartMinute == EndMinute is invalid (rejected by LoadWindow): it is ambiguous
// between "never" and "always" and must not silently mean zero protection.
//
// v1: same window every calendar day (fully periodic within any horizon).
// Periodicity is enforced by the type — LocalWindow has no date fields, so
// deferred effective-from/until dates are unrepresentable (R3).
type LocalWindow struct {
	Location    *time.Location
	StartMinute int
	EndMinute   int
}

// LoadWindow builds a LocalWindow from an IANA name and minute-of-day bounds.
// Rejects empty windows (start == end). Use a full-day window as 0..1440 is not
// representable half-open; callers wanting 24h should use ExtraBlocks instead
// or StartMinute=0, EndMinute=1439 for "almost full day" — prefer ExtraBlocks
// for always-on blocks.
func LoadWindow(tz string, startMin, endMin int) (LocalWindow, error) {
	if startMin < 0 || startMin >= 1440 || endMin < 0 || endMin >= 1440 {
		return LocalWindow{}, fmt.Errorf("minutes out of range [0,1440)")
	}
	if startMin == endMin {
		return LocalWindow{}, fmt.Errorf("empty window: start and end minutes must differ (use ExtraBlocks for always-on)")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return LocalWindow{}, err
	}
	return LocalWindow{Location: loc, StartMinute: startMin, EndMinute: endMin}, nil
}

// localMidnight returns the civil midnight on the calendar day of t in loc.
func localMidnight(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// wallClockOnDay maps a minute-of-day onto civil day d (local midnight).
// Non-existent times (spring-forward gap) are normalized by time.Date (forward).
// Ambiguous times (fall-back) use Go's first occurrence; documented tradeoff.
func wallClockOnDay(dayMidnight time.Time, minute int) time.Time {
	return time.Date(
		dayMidnight.Year(), dayMidnight.Month(), dayMidnight.Day(),
		minute/60, minute%60, 0, 0, dayMidnight.Location(),
	)
}

// ActiveAt reports whether t falls inside the window.
func (w LocalWindow) ActiveAt(t time.Time) bool {
	if w.Location == nil || w.StartMinute == w.EndMinute {
		return false
	}
	local := t.In(w.Location)
	m := local.Hour()*60 + local.Minute()
	if w.StartMinute < w.EndMinute {
		return m >= w.StartMinute && m < w.EndMinute
	}
	// Overnight: e.g. 20:00–08:00
	return m >= w.StartMinute || m < w.EndMinute
}

// Breakpoints returns UTC instants in [from, to] where the window may toggle.
// Advances by calendar day (AddDate), not 24h duration, so DST transitions
// do not shift subsequent midnights (S1).
func (w LocalWindow) Breakpoints(from, to time.Time) []time.Time {
	if w.Location == nil || !to.After(from) {
		return []time.Time{from.UTC().Truncate(time.Minute)}
	}
	seen := map[int64]struct{}{}
	var out []time.Time
	add := func(t time.Time) {
		if t.Before(from) || t.After(to) {
			return
		}
		t = t.UTC().Truncate(time.Minute)
		key := t.Unix()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	add(from)

	day := localMidnight(from, w.Location)
	// Include the calendar day of `to` and one extra so overnight edges are covered.
	lastDay := localMidnight(to, w.Location).AddDate(0, 0, 1)

	for d := day; !d.After(lastDay); d = d.AddDate(0, 0, 1) {
		add(wallClockOnDay(d, w.StartMinute))
		add(wallClockOnDay(d, w.EndMinute))
	}
	return out
}

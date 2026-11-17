package api

import (
	"fmt"
	"strings"
	"time"
)

// Weekday abbreviations the maintenance window accepts, in
// time.Weekday order (Sunday first). Matching is case-insensitive;
// "mon", "Mon" and "MON" are the same day.
var weekdayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// parseWeekday turns a three-letter abbreviation into a
// time.Weekday. Anything else — a full name, a number, a typo —
// is an error, so a manifest cannot silently schedule a window on
// a day that never comes.
func parseWeekday(s string) (time.Weekday, error) {
	for i, name := range weekdayNames {
		if strings.EqualFold(s, name) {
			return time.Weekday(i), nil
		}
	}
	return time.Sunday, fmt.Errorf("%q is not a weekday abbreviation (Mon, Tue, Wed, Thu, Fri, Sat, Sun)", s)
}

// parseHHMM turns a "HH:MM" 24-hour time into minutes since
// midnight. The format is strict on purpose: "2pm", "25:00" and
// "02:60" are all rejected instead of guessed at, because a window
// that opens at the wrong hour deploys at the wrong hour.
func parseHHMM(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("%q must be \"HH:MM\" (24-hour UTC, e.g. \"02:00\")", s)
	}
	// Sscanf accepts "2:0" and "02:00:00" alike; require the exact
	// shape so "02:00:00" does not silently mean 02:00.
	if len(s) != 5 || s[2] != ':' || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("%q must be \"HH:MM\" (24-hour UTC, e.g. \"02:00\")", s)
	}
	return h*60 + m, nil
}

// windowDurationMinutes is how long one day's window lasts: end
// minus start, or the overnight remainder when end is not after
// start. Start == end is 0 here and is rejected by validation —
// a window that opens and closes at the same minute never opens.
func windowDurationMinutes(start, end int) int {
	if end > start {
		return end - start
	}
	return 24*60 - (start - end)
}

// windowDays parses and validates the Days list. An empty list
// means every day; anything else must be all valid abbreviations.
// The returned set is what ActiveAt consults.
func (w *MaintenanceWindowSpec) windowDays() (map[time.Weekday]bool, error) {
	days := make(map[time.Weekday]bool, len(w.Days))
	for _, d := range w.Days {
		wd, err := parseWeekday(d)
		if err != nil {
			return nil, err
		}
		days[wd] = true
	}
	return days, nil
}

// windowMinutes parses Start and End into minutes since midnight.
func (w *MaintenanceWindowSpec) windowMinutes() (start, end int, err error) {
	start, err = parseHHMM(w.Start)
	if err != nil {
		return 0, 0, fmt.Errorf("spec.maintenanceWindow.start: %w", err)
	}
	end, err = parseHHMM(w.End)
	if err != nil {
		return 0, 0, fmt.Errorf("spec.maintenanceWindow.end: %w", err)
	}
	return start, end, nil
}

// ActiveAt reports whether the window is open at t. A nil window
// is always open (no restriction). Times are UTC: a window is a
// wall-clock agreement between operators, not a local-time one, so
// a team in any timezone reads the same schedule.
//
// A window opens on each listed day (every day when Days is
// empty) at Start and runs for its duration. When End is not after
// Start the window runs overnight, past midnight into the next
// day — so a Mon 22:00–02:00 window is still open at Tue 01:00,
// because that hour belongs to Monday's window, not Tuesday's.
// The duration is always under 24 hours (start == end is
// invalid), so only two candidate windows can ever contain t:
// today's and yesterday's.
func (w *MaintenanceWindowSpec) ActiveAt(t time.Time) bool {
	if w == nil {
		return true
	}
	days, err := w.windowDays()
	if err != nil {
		return false
	}
	start, end, err := w.windowMinutes()
	if err != nil {
		return false
	}
	duration := time.Duration(windowDurationMinutes(start, end)) * time.Minute
	t = t.UTC()
	for _, offset := range []int{0, -1} {
		day := time.Date(t.Year(), t.Month(), t.Day()+offset, 0, 0, 0, 0, time.UTC)
		if len(days) > 0 && !days[day.Weekday()] {
			continue
		}
		open := day.Add(time.Duration(start) * time.Minute)
		if !t.Before(open) && t.Before(open.Add(duration)) {
			return true
		}
	}
	return false
}

// NextOpening returns the next time the window opens strictly after
// t, looking up to 8 days ahead. It is what the reconciler quotes
// when it defers a change ("next window opens Tue 02:00 UTC"), so
// an operator waiting on a deploy knows exactly when to look
// again. The zero time means no opening was found — only possible
// for a window whose Days list has no valid entry, which
// validation rejects before it can reach the reconciler.
func (w *MaintenanceWindowSpec) NextOpening(t time.Time) time.Time {
	if w == nil {
		return time.Time{}
	}
	days, err := w.windowDays()
	if err != nil {
		return time.Time{}
	}
	start, _, err := w.windowMinutes()
	if err != nil {
		return time.Time{}
	}
	t = t.UTC()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		d := day.AddDate(0, 0, i)
		if len(days) > 0 && !days[d.Weekday()] {
			continue
		}
		if open := d.Add(time.Duration(start) * time.Minute); open.After(t) {
			return open
		}
	}
	return time.Time{}
}

// Describe renders the window the way `mlop status` shows it:
// "Mon–Fri 02:00–04:00 UTC", "daily 22:00–02:00 UTC", or "" for
// a nil window. Day ranges collapse only when the days are
// consecutive with no gaps; anything else lists each day.
func (w *MaintenanceWindowSpec) Describe() string {
	if w == nil {
		return ""
	}
	days := ""
	if len(w.Days) == 0 {
		days = "daily"
	} else {
		// Normalise the order to Mon..Sun for display, keeping
		// the author's abbreviations out of the output.
		seen := map[time.Weekday]bool{}
		for _, d := range w.Days {
			if wd, err := parseWeekday(d); err == nil {
				seen[wd] = true
			}
		}
		order := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
		var names []string
		for _, wd := range order {
			if seen[wd] {
				names = append(names, weekdayNames[wd])
			}
		}
		days = describeDays(names)
	}
	return days + " " + w.Start + "–" + w.End + " UTC"
}

// describeDays collapses a consecutive run of days into a range
// ("Mon–Fri") and lists anything else ("Mon, Wed, Fri"). A single
// day, or all seven, are their own special cases.
func describeDays(names []string) string {
	if len(names) == 0 {
		return "daily"
	}
	if len(names) == 7 {
		return "daily"
	}
	if len(names) == 1 {
		return names[0]
	}
	// Are they consecutive in Mon..Sun order? The input arrives
	// already ordered, so a run means each next day follows the
	// previous one in the week.
	weekdayIndex := map[string]int{"Mon": 0, "Tue": 1, "Wed": 2, "Thu": 3, "Fri": 4, "Sat": 5, "Sun": 6}
	consecutive := true
	for i := 1; i < len(names); i++ {
		if weekdayIndex[names[i]] != weekdayIndex[names[i-1]]+1 {
			consecutive = false
			break
		}
	}
	if consecutive {
		return names[0] + "–" + names[len(names)-1]
	}
	return strings.Join(names, ", ")
}

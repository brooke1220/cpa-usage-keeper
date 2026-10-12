package timeutil

import (
	"fmt"
	"time"
)

// StatisticalDayStart is the inclusive start of the statistical day containing now.
// The day is labeled by that start's calendar date in location. Its window is
// [start, the next occurrence of the same clock time).
func StatisticalDayStart(now time.Time, location *time.Location, workStartMinute int) time.Time {
	if location == nil {
		location = time.UTC
	}
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), workStartMinute/60, workStartMinute%60, 0, 0, location)
	if local.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return start
}

// ClockOnDate returns the given minute-of-day on day's calendar date.
func ClockOnDate(day time.Time, minute int, location *time.Location) time.Time {
	if location == nil {
		location = day.Location()
	}
	return time.Date(day.Year(), day.Month(), day.Day(), minute/60, minute%60, 0, 0, location)
}

// FormatClockMinute renders a minute-of-day, including 24:00, as HH:mm.
func FormatClockMinute(minute int) string {
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}

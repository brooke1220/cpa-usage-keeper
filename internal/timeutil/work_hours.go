package timeutil

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseWorkHours accepts a half-open, same-day window. End may be 24:00.
func ParseWorkHours(start, end string) (int, int, error) {
	parse := func(value string, allowEndOfDay bool) (int, error) {
		value = strings.TrimSpace(value)
		if len(value) != 5 || value[2] != ':' {
			return 0, fmt.Errorf("use HH:mm")
		}
		for _, index := range []int{0, 1, 3, 4} {
			if value[index] < '0' || value[index] > '9' {
				return 0, fmt.Errorf("use HH:mm")
			}
		}
		hour, _ := strconv.Atoi(value[:2])
		minute, _ := strconv.Atoi(value[3:])
		if allowEndOfDay && hour == 24 && minute == 0 {
			return 1440, nil
		}
		if hour > 23 || minute > 59 {
			return 0, fmt.Errorf("time is outside the valid range")
		}
		return hour*60 + minute, nil
	}
	first, err := parse(start, false)
	if err != nil {
		return 0, 0, fmt.Errorf("work start: %w", err)
	}
	last, err := parse(end, true)
	if err != nil {
		return 0, 0, fmt.Errorf("work end: %w", err)
	}
	if first >= last {
		return 0, 0, fmt.Errorf("work start must be before work end on the same day")
	}
	return first, last, nil
}

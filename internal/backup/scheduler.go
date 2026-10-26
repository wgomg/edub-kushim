package backup

import (
	"time"
)

// ScheduleTiming is a schedule's serialized form: interval in days and
// the preferred time of day.
type ScheduleTiming struct {
	Interval float64 `json:"interval"`
	Time     string  `json:"time"`
}

func parsePreferredTime(preferredTime string) time.Time {
	pref := "02:00"
	if preferredTime != "" {
		pref = preferredTime
	}
	prefTime, err := time.Parse("15:04", pref)
	if err != nil {
		prefTime, _ = time.Parse("15:04", "02:00")
	}
	return prefTime
}

// NextBackupTime returns the first occurrence strictly after the given
// instant, honoring the preferred time of day; a sub-daily interval adds
// a day once the same-day occurrences are past. Zero interval clamps to
// one day; an empty or malformed time defaults to 02:00.
func NextBackupTime(after time.Time, intervalDays float64, preferredTime string) time.Time {
	if intervalDays <= 0 {
		intervalDays = 1
	}

	prefTime := parsePreferredTime(preferredTime)

	loc := after.Location()

	if intervalDays >= 1.0 {
		refDay := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, loc)
		targetDay := refDay.AddDate(0, 0, int(intervalDays))
		next := time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(),
			prefTime.Hour(), prefTime.Minute(), 0, 0, loc)
		// contract: never return a time at or before `after` (observed same-day next)
		for !next.After(after) {
			next = next.AddDate(0, 0, 1)
		}
		return next
	}

	intervalDur := time.Duration(intervalDays * 24 * float64(time.Hour))
	candidate := time.Date(after.Year(), after.Month(), after.Day(),
		prefTime.Hour(), prefTime.Minute(), 0, 0, loc)
	for !candidate.After(after) {
		candidate = candidate.Add(intervalDur)
	}
	return candidate
}

// NextOccurrence returns the next occurrence of the preferred time of day strictly after the given instant:
// today's slot when it is still ahead, otherwise the next day's.
func NextOccurrence(now time.Time, preferredTime string) time.Time {
	prefTime := parsePreferredTime(preferredTime)
	loc := now.Location()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), prefTime.Hour(), prefTime.Minute(), 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

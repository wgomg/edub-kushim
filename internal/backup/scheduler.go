package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
)

type ScheduleTiming struct {
	Interval float64 `json:"interval"`
	Time     string  `json:"time"`
}

func (t ScheduleTiming) Equal(other ScheduleTiming) bool {
	return t.Interval == other.Interval && preferredMinutes(t.Time) == preferredMinutes(other.Time)
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

func preferredMinutes(preferredTime string) int {
	prefTime := parsePreferredTime(preferredTime)
	return prefTime.Hour()*60 + prefTime.Minute()
}

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

func NextScheduledTime(lastRun time.Time, intervalDays float64, preferredTime string) time.Time {
	if intervalDays >= 1.0 {
		return nextOccurrence(lastRun, preferredTime)
	}
	return NextBackupTime(lastRun, intervalDays, preferredTime)
}

func nextOccurrence(now time.Time, preferredTime string) time.Time {
	prefTime := parsePreferredTime(preferredTime)
	loc := now.Location()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), prefTime.Hour(), prefTime.Minute(), 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func FirstRunAnchor(now time.Time, intervalDays float64, preferredTime string) time.Time {
	if intervalDays <= 0 {
		intervalDays = 1
	}
	first := nextOccurrence(now, preferredTime)
	if intervalDays < 1 {
		return first.Add(-time.Duration(intervalDays * 24 * float64(time.Hour)))
	}
	return first.AddDate(0, 0, -int(intervalDays))
}

func IsBackupDue(ctx context.Context, queries *database.Queries, schedule config.BackupSchedule) (bool, error) {
	recent, err := queries.CountRecentBackupTasksByMode(ctx, database.CountRecentBackupTasksByModeParams{
		Column1: sql.NullString{String: "5", Valid: true},
		Column2: sql.NullString{String: schedule.Mode, Valid: true},
	})
	if err != nil {
		return false, err
	}

	last, err := queries.GetLastCompletedBackupByMode(ctx, sql.NullString{String: schedule.Mode, Valid: true})
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}

	return dueFromHistory(recent, last.CompletedAt, schedule.Interval, schedule.Time, scheduleTimingFromPayload(last.Payload)), nil
}

func IsMirrorDue(ctx context.Context, queries *database.Queries, schedule config.MirrorConfig) (bool, error) {
	recent, err := queries.CountRecentMirrorTasks(ctx, sql.NullString{String: "5", Valid: true})
	if err != nil {
		return false, err
	}

	last, err := queries.GetLastCompletedMirror(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}

	return dueFromHistory(recent, last.CompletedAt, schedule.Interval, schedule.Time, scheduleTimingFromPayload(last.Payload)), nil
}

func scheduleTimingFromPayload(payload *json.RawMessage) *ScheduleTiming {
	if payload == nil {
		return nil
	}
	var p struct {
		Schedule *ScheduleTiming `json:"schedule"`
	}
	if err := json.Unmarshal(*payload, &p); err != nil {
		return nil
	}
	return p.Schedule
}

func dueFromHistory(recent int64, completedAt sql.NullTime, interval float64, preferredTime string, lastTiming *ScheduleTiming) bool {
	if recent > 0 {
		return false
	}
	if !completedAt.Valid {
		return true
	}
	next := NextBackupTime(completedAt.Time, interval, preferredTime)
	if lastTiming != nil && !lastTiming.Equal(ScheduleTiming{Interval: interval, Time: preferredTime}) {
		next = NextScheduledTime(completedAt.Time, interval, preferredTime)
	}
	return !time.Now().Before(next)
}

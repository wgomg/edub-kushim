package backup

import (
	"context"
	"database/sql"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
)

func NextBackupTime(after time.Time, intervalDays float64, preferredTime string) time.Time {
	if intervalDays <= 0 {
		intervalDays = 1
	}

	pref := "02:00"
	if preferredTime != "" {
		pref = preferredTime
	}
	prefTime, err := time.Parse("15:04", pref)
	if err != nil {
		prefTime, _ = time.Parse("15:04", "02:00")
	}

	loc := after.Location()

	if intervalDays >= 1.0 {
		refDay := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, loc)
		targetDay := refDay.AddDate(0, 0, int(intervalDays))
		return time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(),
			prefTime.Hour(), prefTime.Minute(), 0, 0, loc)
	}

	intervalDur := time.Duration(intervalDays * 24 * float64(time.Hour))
	candidate := time.Date(after.Year(), after.Month(), after.Day(),
		prefTime.Hour(), prefTime.Minute(), 0, 0, loc)
	for !candidate.After(after) {
		candidate = candidate.Add(intervalDur)
	}
	return candidate
}

func IsBackupDue(ctx context.Context, queries *database.Queries, schedule config.BackupSchedule) (bool, error) {
	recent, err := queries.CountRecentBackupTasksByMode(ctx, database.CountRecentBackupTasksByModeParams{
		Column1: sql.NullString{String: "5", Valid: true},
		Column2: sql.NullString{String: schedule.Mode, Valid: true},
	})
	if err != nil {
		return false, err
	}

	completedAt, err := queries.GetLastCompletedBackupByMode(ctx, sql.NullString{String: schedule.Mode, Valid: true})
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}

	return dueFromHistory(recent, completedAt, schedule.Interval, schedule.Time), nil
}

func IsMirrorDue(ctx context.Context, queries *database.Queries, schedule config.MirrorConfig) (bool, error) {
	recent, err := queries.CountRecentMirrorTasks(ctx, sql.NullString{String: "5", Valid: true})
	if err != nil {
		return false, err
	}

	completedAt, err := queries.GetLastCompletedMirror(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}

	return dueFromHistory(recent, completedAt, schedule.Interval, schedule.Time), nil
}

func dueFromHistory(recent int64, completedAt sql.NullTime, interval float64, preferredTime string) bool {
	if recent > 0 {
		return false
	}
	if !completedAt.Valid {
		return true
	}
	next := NextBackupTime(completedAt.Time, interval, preferredTime)
	return !time.Now().Before(next)
}

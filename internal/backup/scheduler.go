package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type BackupState struct {
	NextScheduled string `json:"next_scheduled"`
}

func ReadState(configDir string) (*BackupState, error) {
	path := filepath.Join(configDir, "backup-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &BackupState{}, nil
		}
		return nil, fmt.Errorf("read backup state: %w", err)
	}
	var s BackupState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal backup state: %w", err)
	}
	return &s, nil
}

func WriteState(configDir string, s *BackupState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup state: %w", err)
	}
	path := filepath.Join(configDir, "backup-state.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write backup state: %w", err)
	}
	return nil
}

func NextRunTime(intervalDays float64, preferredTime string) time.Time {
	now := time.Now()

	pref := "02:00"
	if preferredTime != "" {
		pref = preferredTime
	}
	prefTime, err := time.Parse("15:04", pref)
	if err != nil {
		prefTime, _ = time.Parse("15:04", "02:00")
	}

	candidate := time.Date(now.Year(), now.Month(), now.Day(), prefTime.Hour(), prefTime.Minute(), 0, 0, now.Location())

	if candidate.Before(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}

	intervalDur := time.Duration(intervalDays * 24 * float64(time.Hour))
	if candidate.Sub(now) < intervalDur {
		return candidate
	}

	days := int(intervalDays)
	remaining := intervalDays - float64(days)
	candidate = time.Date(now.Year(), now.Month(), now.Day(), prefTime.Hour(), prefTime.Minute(), 0, 0, now.Location())
	for candidate.Before(now) || candidate.Sub(now) < intervalDur {
		candidate = candidate.AddDate(0, 0, days)
		if remaining > 0 {
			candidate = candidate.Add(time.Duration(remaining * 24 * float64(time.Hour)))
		}
	}

	return candidate
}

func ShouldSchedule(state *BackupState, intervalDays float64, preferredTime string) bool {
	if state.NextScheduled == "" {
		return true
	}
	next, err := time.Parse(time.RFC3339, state.NextScheduled)
	if err != nil {
		return true
	}
	return time.Now().After(next) || time.Now().Equal(next)
}

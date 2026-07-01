package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShouldSchedule_EmptyState(t *testing.T) {
	state := &BackupState{}
	if !ShouldSchedule(state, 1, "02:00") {
		t.Error("ShouldSchedule() = false for empty state, want true")
	}
}

func TestShouldSchedule_PastTime(t *testing.T) {
	state := &BackupState{
		NextScheduled: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	if !ShouldSchedule(state, 1, "02:00") {
		t.Error("ShouldSchedule() = false for past time, want true")
	}
}

func TestShouldSchedule_FutureTime(t *testing.T) {
	state := &BackupState{
		NextScheduled: time.Now().Add(1 * time.Hour).Format(time.RFC3339),
	}
	if ShouldSchedule(state, 1, "02:00") {
		t.Error("ShouldSchedule() = true for future time, want false")
	}
}

func TestShouldSchedule_InvalidFormat(t *testing.T) {
	state := &BackupState{
		NextScheduled: "not-a-valid-time",
	}
	if !ShouldSchedule(state, 1, "02:00") {
		t.Error("ShouldSchedule() = false for invalid format, want true")
	}
}

func TestNextRunTime_FutureToday(t *testing.T) {
	now := time.Now()
	hour := min(now.Hour()+2, 23)

	result := NextRunTime(1, time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location()).Format("15:04"))

	if result.Before(now) {
		t.Errorf("NextRunTime() = %v, want >= %v", result, now)
	}
	if result.Hour() != hour || result.Minute() != 0 {
		t.Errorf("NextRunTime() = %v, want hour=%d minute=0", result, hour)
	}
}

func TestNextRunTime_PastToday(t *testing.T) {
	now := time.Now()
	hour := max(now.Hour()-2, 0)

	result := NextRunTime(1, time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location()).Format("15:04"))

	if result.Before(now) {
		t.Errorf("NextRunTime() = %v, want >= %v (should be tomorrow or later)", result, now)
	}
}

func TestNextRunTime_DefaultFallback(t *testing.T) {
	result := NextRunTime(1, "")
	if result.Before(time.Now()) {
		t.Errorf("NextRunTime() = %v, want >= now", result)
	}
}

func TestReadWriteState_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	state := &BackupState{NextScheduled: "2026-07-01T02:00:00Z"}
	if err := WriteState(dir, state); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	got, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.NextScheduled != state.NextScheduled {
		t.Errorf("NextScheduled = %q, want %q", got.NextScheduled, state.NextScheduled)
	}
}

func TestReadState_Missing(t *testing.T) {
	got, err := ReadState(t.TempDir())
	if err != nil {
		t.Fatalf("ReadState on missing file: %v", err)
	}
	if got.NextScheduled != "" {
		t.Errorf("NextScheduled = %q, want empty", got.NextScheduled)
	}
}

func TestReadState_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "backup-state.json"), []byte("{bad"), 0600)

	_, err := ReadState(dir)
	if err == nil {
		t.Fatal("ReadState() expected error for invalid JSON")
	}
}

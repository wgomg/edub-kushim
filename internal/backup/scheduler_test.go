package backup

import (
	"testing"
	"time"
)

func TestNextBackupTime_DailySameDay(t *testing.T) {
	after := time.Date(2026, 7, 28, 0, 57, 7, 0, time.UTC)
	result := NextBackupTime(after, 1, "01:30")
	expected := time.Date(2026, 7, 29, 1, 30, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("NextBackupTime = %v, want %v", result, expected)
	}
}

func TestNextBackupTime_DailyAfterPreferred(t *testing.T) {
	after := time.Date(2026, 7, 28, 1, 30, 35, 0, time.UTC)
	result := NextBackupTime(after, 1, "01:30")
	expected := time.Date(2026, 7, 29, 1, 30, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("NextBackupTime = %v, want %v (no skipped day)", result, expected)
	}
}

func TestNextBackupTime_MultiDay(t *testing.T) {
	after := time.Date(2026, 7, 27, 1, 30, 0, 0, time.UTC)
	result := NextBackupTime(after, 2, "02:00")
	expected := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("NextBackupTime = %v, want %v", result, expected)
	}
}

func TestNextBackupTime_SubDaily(t *testing.T) {
	after := time.Date(2026, 7, 28, 6, 0, 0, 0, time.UTC)
	result := NextBackupTime(after, 0.5, "08:00")
	expected := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("NextBackupTime = %v, want %v", result, expected)
	}
}

func TestNextBackupTime_SubDailyWraps(t *testing.T) {
	after := time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)
	result := NextBackupTime(after, 0.5, "08:00")
	expected := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("NextBackupTime = %v, want %v", result, expected)
	}
}

func TestNextBackupTime_ZeroIntervalClampsToOne(t *testing.T) {
	after := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	result := NextBackupTime(after, 0, "02:00")
	expected := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("NextBackupTime(0) = %v, want %v (should clamp to 1 day)", result, expected)
	}
}

func TestNextBackupTime_EmptyPreferredTimeDefaultsTo0200(t *testing.T) {
	after := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	result := NextBackupTime(after, 1, "")
	expected := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("NextBackupTime(\"\") = %v, want %v (default 02:00)", result, expected)
	}
}

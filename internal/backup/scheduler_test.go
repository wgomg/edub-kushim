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

func TestNextBackupTime_DailyStrictlyAfter(t *testing.T) {
	loc := time.FixedZone("EDT", -4*60*60)
	after := time.Date(2026, 9, 5, 22, 49, 47, 0, loc)
	result := NextBackupTime(after, 1, "02:30")
	expected := time.Date(2026, 9, 6, 2, 30, 0, 0, loc)
	if !result.Equal(expected) {
		t.Errorf("NextBackupTime = %v, want %v", result, expected)
	}
	if !result.After(after) {
		t.Errorf("NextBackupTime = %v, must be strictly after %v", result, after)
	}
}

// TestNextOccurrence pins the first-run slot: the next preferred time of day
// strictly after the given instant, so a never-run schedule waits for its slot
// instead of firing on evaluation.
func TestNextOccurrence(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		pref string
		want time.Time
	}{
		{
			name: "daily before preferred time",
			now:  time.Date(2026, 7, 28, 0, 57, 0, 0, time.UTC),
			pref: "01:30",
			want: time.Date(2026, 7, 28, 1, 30, 0, 0, time.UTC),
		},
		{
			name: "daily after preferred time",
			now:  time.Date(2026, 7, 28, 1, 31, 0, 0, time.UTC),
			pref: "01:30",
			want: time.Date(2026, 7, 29, 1, 30, 0, 0, time.UTC),
		},
		{
			name: "weekly before preferred time",
			now:  time.Date(2026, 7, 28, 18, 49, 0, 0, time.UTC),
			pref: "18:55",
			want: time.Date(2026, 7, 28, 18, 55, 0, 0, time.UTC),
		},
		{
			name: "empty pref defaults to 02:00",
			now:  time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
			pref: "",
			want: time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC),
		},
		{
			name: "invalid pref falls back to 02:00",
			now:  time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
			pref: "garbage",
			want: time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC),
		},
		{
			name: "fixed offset zone",
			now:  time.Date(2026, 9, 5, 22, 49, 0, 0, time.FixedZone("EDT", -4*60*60)),
			pref: "02:30",
			want: time.Date(2026, 9, 6, 2, 30, 0, 0, time.FixedZone("EDT", -4*60*60)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextOccurrence(tt.now, tt.pref)
			if !got.Equal(tt.want) {
				t.Errorf("NextOccurrence(%v, %q) = %v, want %v", tt.now, tt.pref, got, tt.want)
			}
			if !got.After(tt.now) {
				t.Errorf("NextOccurrence(%v, %q) = %v, must be strictly after now", tt.now, tt.pref, got)
			}
		})
	}
}

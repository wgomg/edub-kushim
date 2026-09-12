package backup

import (
	"database/sql"
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

func TestDueFromHistory_RecentBlocks(t *testing.T) {
	if due := dueFromHistory(1, sql.NullTime{}, 1, "02:00"); due {
		t.Error("dueFromHistory with recent>0 must return false")
	}
}

func TestDueFromHistory_NullCompletedDue(t *testing.T) {
	if due := dueFromHistory(0, sql.NullTime{}, 1, "02:00"); !due {
		t.Error("dueFromHistory with no completed run must return true")
	}
}

func TestDueFromHistory_RecentCompletedBeforeWindow(t *testing.T) {
	completed := time.Now().UTC().Add(-30 * time.Minute)
	if due := dueFromHistory(0, sql.NullTime{Time: completed, Valid: true}, 1, time.Now().UTC().Add(time.Hour).Format("15:04")); due {
		t.Error("dueFromHistory with daily interval and recent completion must return false")
	}
}

func TestDueFromHistory_RecentCompletedAfterWindow(t *testing.T) {
	completed := time.Now().UTC().Add(-3 * time.Hour)
	past := time.Now().UTC().Add(-2 * time.Hour).Format("15:04")
	if due := dueFromHistory(0, sql.NullTime{Time: completed, Valid: true}, 0.5, past); !due {
		t.Error("dueFromHistory with sub-daily interval past next-window must return true")
	}
}

// TestFirstRunAnchor_RoundTrip verifies that FirstRunAnchor produces a virtual
// anchor V such that NextBackupTime(V, interval, pref) lands exactly on the
// next occurrence of the preferred time after now.
func TestFirstRunAnchor_RoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		interval float64
		pref     string
		want     time.Time
	}{
		{
			name:     "daily before preferred time",
			now:      time.Date(2026, 7, 28, 0, 57, 0, 0, time.UTC),
			interval: 1,
			pref:     "01:30",
			want:     time.Date(2026, 7, 28, 1, 30, 0, 0, time.UTC),
		},
		{
			name:     "daily after preferred time",
			now:      time.Date(2026, 7, 28, 1, 31, 0, 0, time.UTC),
			interval: 1,
			pref:     "01:30",
			want:     time.Date(2026, 7, 29, 1, 30, 0, 0, time.UTC),
		},
		{
			name:     "weekly before preferred time",
			now:      time.Date(2026, 7, 28, 18, 49, 0, 0, time.UTC),
			interval: 7,
			pref:     "18:55",
			want:     time.Date(2026, 7, 28, 18, 55, 0, 0, time.UTC),
		},
		{
			name:     "weekly after preferred time",
			now:      time.Date(2026, 7, 28, 19, 10, 0, 0, time.UTC),
			interval: 7,
			pref:     "18:55",
			want:     time.Date(2026, 7, 29, 18, 55, 0, 0, time.UTC),
		},
		{
			name:     "two-day interval",
			now:      time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
			interval: 2,
			pref:     "02:00",
			want:     time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC),
		},
		{
			name:     "empty pref defaults to 02:00",
			now:      time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
			interval: 1,
			pref:     "",
			want:     time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC),
		},
		{
			name:     "invalid pref falls back to 02:00",
			now:      time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
			interval: 1,
			pref:     "garbage",
			want:     time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC),
		},
		{
			name:     "sub-daily 0.5 before preferred",
			now:      time.Date(2026, 7, 28, 6, 0, 0, 0, time.UTC),
			interval: 0.5,
			pref:     "08:00",
			want:     time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC),
		},
		{
			name:     "sub-daily 0.5 after preferred",
			now:      time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC),
			interval: 0.5,
			pref:     "08:00",
			want:     time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
		},
		{
			name:     "fixed offset zone",
			now:      time.Date(2026, 9, 5, 22, 49, 0, 0, time.FixedZone("EDT", -4*60*60)),
			interval: 1,
			pref:     "02:30",
			want:     time.Date(2026, 9, 6, 2, 30, 0, 0, time.FixedZone("EDT", -4*60*60)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			anchor := FirstRunAnchor(tt.now, tt.interval, tt.pref)
			got := NextBackupTime(anchor, tt.interval, tt.pref)
			if !got.Equal(tt.want) {
				t.Errorf("NextBackupTime(FirstRunAnchor(%v, %v, %q)) = %v, want %v", tt.now, tt.interval, tt.pref, got, tt.want)
			}
			if !got.After(tt.now) {
				t.Errorf("result %v must be strictly after now %v", got, tt.now)
			}
		})
	}
}

// TestFirstRunAnchor_ZeroIntervalClamps verifies that interval <= 0 is clamped
// to 1, matching NextBackupTime's contract.
func TestFirstRunAnchor_ZeroIntervalClamps(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	anchor := FirstRunAnchor(now, 0, "02:00")
	got := NextBackupTime(anchor, 0, "02:00")
	want := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("FirstRunAnchor(0) → NextBackupTime = %v, want %v", got, want)
	}
	negativeAnchor := FirstRunAnchor(now, -3, "02:00")
	got2 := NextBackupTime(negativeAnchor, -3, "02:00")
	if !got2.Equal(want) {
		t.Errorf("FirstRunAnchor(-3) → NextBackupTime = %v, want %v", got2, want)
	}
}

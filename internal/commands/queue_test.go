package commands

import (
	"testing"
	"time"
)

func TestPreviousTimeOfDay(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		hhmm string
		want time.Time
	}{
		{"after preferred time uses today", time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC), "02:00", time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)},
		{"before preferred time rolls to yesterday", time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC), "02:00", time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC)},
		{"at preferred time uses today", time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC), "02:00", time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)},
		{"empty defaults to 02:00", time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC), "", time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)},
		{"invalid falls back to 02:00", time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC), "garbage", time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC)},
		{"future preferred time rolls to yesterday", time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC), "23:59", time.Date(2026, 8, 22, 23, 59, 0, 0, time.UTC)},
		{"early preferred time already passed", time.Date(2026, 8, 23, 0, 30, 0, 0, time.UTC), "00:15", time.Date(2026, 8, 23, 0, 15, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := previousTimeOfDay(tt.now, tt.hhmm)
			if !got.Equal(tt.want) {
				t.Errorf("previousTimeOfDay(%v, %q) = %v, want %v", tt.now, tt.hhmm, got, tt.want)
			}
		})
	}
}

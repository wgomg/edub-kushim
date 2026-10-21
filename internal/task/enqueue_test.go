package task

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/types"
)

func TestNewBatchSpec(t *testing.T) {
	ok := []struct {
		name     string
		origin   types.BatchOrigin
		schedule string
	}{
		{"cli without schedule", types.Batch.Origin.CLI, ""},
		{"api without schedule", types.Batch.Origin.API, ""},
		{"polling without schedule", types.Batch.Origin.Polling, ""},
		{"setup without schedule", types.Batch.Origin.Setup, ""},
		{"wizard without schedule", types.Batch.Origin.Wizard, ""},
		{"scheduler with schedule name", types.Batch.Origin.Schedule, "nightly-backup"},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			spec, err := NewBatchSpec(c.origin, c.schedule)
			if err != nil {
				t.Fatalf("NewBatchSpec(%q, %q) error: %v", c.origin, c.schedule, err)
			}
			if spec.Origin() != c.origin {
				t.Errorf("Origin() = %q, want %q", spec.Origin(), c.origin)
			}
			if spec.Schedule() != c.schedule {
				t.Errorf("Schedule() = %q, want %q", spec.Schedule(), c.schedule)
			}
		})
	}

	bad := []struct {
		name     string
		origin   types.BatchOrigin
		schedule string
	}{
		{"empty origin", "", ""},
		{"unknown origin", "cron", ""},
		{"schedule origin without a name", types.Batch.Origin.Schedule, ""},
		{"cli origin with a schedule name", types.Batch.Origin.CLI, "nightly-backup"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewBatchSpec(c.origin, c.schedule); err == nil {
				t.Errorf("NewBatchSpec(%q, %q) expected an error", c.origin, c.schedule)
			}
		})
	}
}

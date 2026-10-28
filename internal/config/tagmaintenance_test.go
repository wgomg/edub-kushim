package config

import (
	"strings"
	"testing"
)

// TestDefaultConfig_TagMaintenance pins the maintenance routine's schedule defaults the settings UI and
// the queue daemon read: disabled out of the box, weekly at 02:00.
func TestDefaultConfig_TagMaintenance(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Enricher.TagMaintenance.Enabled {
		t.Error("TagMaintenance.Enabled should default to false")
	}
	if cfg.Enricher.TagMaintenance.Interval != 7 {
		t.Errorf("TagMaintenance.Interval = %v, want 7", cfg.Enricher.TagMaintenance.Interval)
	}
	if cfg.Enricher.TagMaintenance.Time != "02:00" {
		t.Errorf("TagMaintenance.Time = %q, want 02:00", cfg.Enricher.TagMaintenance.Time)
	}
}

// TestFinalizeConfig_TagMaintenance pins the routine's healing and validation rules: a non-positive
// interval and an empty time fall back to the defaults, an explicit schedule is kept, and an
// unparsable time refuses the configuration.
func TestFinalizeConfig_TagMaintenance(t *testing.T) {
	t.Run("non-positive interval resets to the default", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagMaintenance.Interval = 0
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagMaintenance.Interval != 7 {
			t.Errorf("Interval = %v, want 7 after an explicit 0", cfg.Enricher.TagMaintenance.Interval)
		}
	})

	t.Run("empty time resets to the default", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagMaintenance.Time = ""
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagMaintenance.Time != "02:00" {
			t.Errorf("Time = %q, want 02:00 after clearing", cfg.Enricher.TagMaintenance.Time)
		}
	})

	t.Run("explicit schedule preserved", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagMaintenance.Interval = 3
		cfg.Enricher.TagMaintenance.Time = "05:15"
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagMaintenance.Interval != 3 || cfg.Enricher.TagMaintenance.Time != "05:15" {
			t.Errorf("schedule = %v %q, want 3 05:15",
				cfg.Enricher.TagMaintenance.Interval, cfg.Enricher.TagMaintenance.Time)
		}
	})

	t.Run("unparsable time is refused", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagMaintenance.Time = "99:99"
		err := finalizeConfig(cfg, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "enricher.tag_maintenance.time") {
			t.Fatalf("want the tag_maintenance.time refusal, got %v", err)
		}
	})
}

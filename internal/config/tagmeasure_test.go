package config

import "testing"

func TestDefaultConfig_TagMeasure(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Enricher.TagMeasure.Enabled {
		t.Error("TagMeasure.Enabled should default to false")
	}
	if cfg.Enricher.TagMeasure.Interval != 7 {
		t.Errorf("TagMeasure.Interval = %v, want 7", cfg.Enricher.TagMeasure.Interval)
	}
	if cfg.Enricher.TagMeasure.Time != "03:00" {
		t.Errorf("TagMeasure.Time = %q, want 03:00", cfg.Enricher.TagMeasure.Time)
	}
}

func TestFinalizeConfig_TagMeasure(t *testing.T) {
	for _, interval := range []float64{0, -1} {
		t.Run("interval reset to 7", func(t *testing.T) {
			cfg := DefaultConfig(t.TempDir())
			cfg.Enricher.TagMeasure.Interval = interval
			if err := finalizeConfig(cfg, t.TempDir()); err != nil {
				t.Fatalf("finalizeConfig: %v", err)
			}
			if cfg.Enricher.TagMeasure.Interval != 7 {
				t.Errorf("Interval = %v, want 7", cfg.Enricher.TagMeasure.Interval)
			}
		})
	}

	t.Run("empty time defaults to 03:00", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagMeasure.Time = ""
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagMeasure.Time != "03:00" {
			t.Errorf("Time = %q, want 03:00", cfg.Enricher.TagMeasure.Time)
		}
	})

	t.Run("invalid time rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagMeasure.Time = "25:99"
		if err := finalizeConfig(cfg, t.TempDir()); err == nil {
			t.Error("expected error for time=25:99")
		}
	})

	t.Run("explicit values preserved", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagMeasure.Enabled = true
		cfg.Enricher.TagMeasure.Interval = 3
		cfg.Enricher.TagMeasure.Time = "04:30"
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if !cfg.Enricher.TagMeasure.Enabled ||
			cfg.Enricher.TagMeasure.Interval != 3 ||
			cfg.Enricher.TagMeasure.Time != "04:30" {
			t.Fatalf("explicit values overwritten: %+v", cfg.Enricher.TagMeasure)
		}
	})
}

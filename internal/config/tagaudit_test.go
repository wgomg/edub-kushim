package config

import "testing"

// TestDefaultConfig_TagAudit pins the coverage-audit schedule defaults the settings UI and
// the queue daemon read: disabled out of the box with a 150-document sample.
func TestDefaultConfig_TagAudit(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Enricher.TagAudit.Enabled {
		t.Error("TagAudit.Enabled should default to false")
	}
	if cfg.Enricher.TagAudit.SampleSize != 150 {
		t.Errorf("TagAudit.SampleSize = %v, want 150", cfg.Enricher.TagAudit.SampleSize)
	}
}

// TestFinalizeConfig_TagAuditSampleSize pins the sample-size healing rule: any non-positive
// value falls back to the default, explicit values are kept.
func TestFinalizeConfig_TagAuditSampleSize(t *testing.T) {
	t.Run("non-positive sample size resets to the default", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagAudit.SampleSize = 0
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagAudit.SampleSize != 150 {
			t.Errorf("SampleSize = %v, want 150 after an explicit 0", cfg.Enricher.TagAudit.SampleSize)
		}
	})

	t.Run("explicit sample size preserved", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagAudit.SampleSize = 200
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagAudit.SampleSize != 200 {
			t.Errorf("SampleSize = %v, want 200", cfg.Enricher.TagAudit.SampleSize)
		}
	})
}

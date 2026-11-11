package config

import (
	"strings"
	"testing"
)

// TestFinalizeConfig_TagJudgeValidation pins the load-time contract for the judged sections: a partial
// stanza (only one of provider/model set) is rejected instead of being silently discarded by the resolver;
// an enabled judge with no provider/model to judge with fails at load, as does request_delay beyond the
// loader bound. The adapter is deliberately not required: the factory default treats anything non-anthropic
// as openai-compatible, so previously-working judged sections keep loading.
func TestFinalizeConfig_TagJudgeValidation(t *testing.T) {
	validAdjudicator := func(cfg *Config) {
		cfg.Enricher.TagAdjudicator.Enabled = true
		cfg.Enricher.TagAdjudicator.Llm.Provider = "test"
		cfg.Enricher.TagAdjudicator.Llm.Model = "test-model"
	}
	validQuality := func(cfg *Config) {
		cfg.Enricher.TagQuality.Enabled = true
		cfg.Enricher.TagQuality.Llm.Provider = "test"
		cfg.Enricher.TagQuality.Llm.Model = "test-model"
	}

	t.Run("judged sections disabled skip validation", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
	})

	t.Run("full stanzas without adapter still load", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		validAdjudicator(cfg)
		validQuality(cfg)
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
	})

	t.Run("partial provider-only section is rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagAdjudicator.Enabled = true
		cfg.Enricher.TagAdjudicator.Llm.Provider = "test"

		err := finalizeConfig(cfg, t.TempDir())
		if err == nil {
			t.Fatal("expected an error for a provider-only judged section")
		}
		if want := "enricher.tag_adjudicator.llm requires both provider and model"; !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the partial section %q", err, want)
		}
	})

	t.Run("partial model-only section is rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagQuality.Enabled = true
		cfg.Enricher.TagQuality.Llm.Model = "test-model"

		err := finalizeConfig(cfg, t.TempDir())
		if err == nil {
			t.Fatal("expected an error for a model-only judged section")
		}
		if want := "enricher.tag_quality.llm requires both provider and model"; !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the partial section %q", err, want)
		}
	})

	t.Run("enabled section with no model anywhere is rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagAdjudicator.Enabled = true

		err := finalizeConfig(cfg, t.TempDir())
		if err == nil {
			t.Fatal("expected an error for an enabled judge with nothing to judge with")
		}
		if want := "enricher.tag_adjudicator.llm.provider is required"; !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the missing provider %q", err, want)
		}
	})

	t.Run("section inherits the analyzer model when own stanza is empty", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.ContentAnalyzer.Llm.Adapter = "openai-compatible"
		cfg.Enricher.ContentAnalyzer.Llm.Provider = "test"
		cfg.Enricher.ContentAnalyzer.Llm.Model = "test-model"
		cfg.Enricher.TagQuality.Enabled = true

		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
	})

	t.Run("request delay beyond the loader bound is rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		validAdjudicator(cfg)
		cfg.Enricher.TagQuality.Llm = cfg.Enricher.TagAdjudicator.Llm
		cfg.Enricher.TagQuality.Llm.RequestDelay = maxRequestDelaySeconds + 1
		cfg.Enricher.TagQuality.Enabled = true

		err := finalizeConfig(cfg, t.TempDir())
		if err == nil {
			t.Fatal("expected an error for request_delay beyond the loader bound")
		}
		if want := "enricher.tag_quality.llm.request_delay must be between 0 and 60"; !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the delay bound %q", err, want)
		}
	})

	t.Run("negative request delay is rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		validAdjudicator(cfg)
		cfg.Enricher.TagAdjudicator.Llm.RequestDelay = -1

		err := finalizeConfig(cfg, t.TempDir())
		if err == nil {
			t.Fatal("expected an error for negative request_delay")
		}
		if want := "enricher.tag_adjudicator.llm.request_delay must be between 0 and 60"; !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the delay bound %q", err, want)
		}
	})
}

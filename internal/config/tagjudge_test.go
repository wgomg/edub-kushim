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
	validGate := func(cfg *Config) {
		cfg.Enricher.TagLexicalGate.Llm.Provider = "test"
		cfg.Enricher.TagLexicalGate.Llm.Model = "test-model"
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
		validGate(cfg)
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
		validGate(cfg)
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

// TestResolvedLexicalGateLlm_ChainPrefersOwnAdjudicatorAnalyzer pins the gate's fallback chain: its
// own stanza wins, then the adjudicator's, then the analyzer's. The middle link is the contract that
// keeps an adjudicator-only configuration usable for the gate passes instead of resolving an empty
// endpoint only the loader would reject.
func TestResolvedLexicalGateLlm_ChainPrefersOwnAdjudicatorAnalyzer(t *testing.T) {
	analyzerOnly := func(cfg *Config) {
		cfg.Enricher.ContentAnalyzer.Llm.Provider = "analyzer"
		cfg.Enricher.ContentAnalyzer.Llm.Model = "analyzer-model"
	}
	adjudicatorOnly := func(cfg *Config) {
		cfg.Enricher.TagAdjudicator.Llm.Provider = "adjudicator"
		cfg.Enricher.TagAdjudicator.Llm.Model = "adjudicator-model"
	}

	t.Run("own stanza wins over both links", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		analyzerOnly(cfg)
		adjudicatorOnly(cfg)
		cfg.Enricher.TagLexicalGate.Llm.Provider = "gate"
		cfg.Enricher.TagLexicalGate.Llm.Model = "gate-model"

		got := cfg.ResolvedLexicalGateLlm()
		if got.Provider != "gate" || got.Model != "gate-model" {
			t.Errorf("resolved = %s/%s, want the gate's own stanza", got.Provider, got.Model)
		}
	})

	t.Run("adjudicator stanza wins when the gate stanza is empty", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		analyzerOnly(cfg)
		adjudicatorOnly(cfg)

		got := cfg.ResolvedLexicalGateLlm()
		if got.Provider != "adjudicator" || got.Model != "adjudicator-model" {
			t.Errorf("resolved = %s/%s, want the adjudicator fallback", got.Provider, got.Model)
		}
	})

	t.Run("analyzer stanza wins when gate and adjudicator are empty", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		analyzerOnly(cfg)

		got := cfg.ResolvedLexicalGateLlm()
		if got.Provider != "analyzer" || got.Model != "analyzer-model" {
			t.Errorf("resolved = %s/%s, want the analyzer fallback", got.Provider, got.Model)
		}
	})
}

// TestResolvedLexicalGateTimeout_ChainPrefersOwnAdjudicatorAnalyzer pins the timeout fallback chain:
// a set gate timeout wins, then the adjudicator's, then the analyzer's.
func TestResolvedLexicalGateTimeout_ChainPrefersOwnAdjudicatorAnalyzer(t *testing.T) {
	t.Run("own timeout wins over both links", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.ContentAnalyzer.Timeout = 10
		cfg.Enricher.TagAdjudicator.Timeout = 20
		cfg.Enricher.TagLexicalGate.Timeout = 30

		if got := cfg.ResolvedLexicalGateTimeout(); got != 30 {
			t.Errorf("resolved timeout = %d, want 30", got)
		}
	})

	t.Run("adjudicator timeout wins when the gate timeout is unset", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.ContentAnalyzer.Timeout = 10
		cfg.Enricher.TagAdjudicator.Timeout = 20

		if got := cfg.ResolvedLexicalGateTimeout(); got != 20 {
			t.Errorf("resolved timeout = %d, want 20", got)
		}
	})

	t.Run("analyzer timeout wins when gate and adjudicator timeouts are unset", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.ContentAnalyzer.Timeout = 10

		if got := cfg.ResolvedLexicalGateTimeout(); got != 10 {
			t.Errorf("resolved timeout = %d, want 10", got)
		}
	})
}

// TestFinalizeConfig_DegradedAuditModeWithoutJudge pins the audit and maintenance schedules' degraded
// contract: with their toggles on and no endpoint configured anywhere, load succeeds without a judge
// (the runner logs and stays deferred), while a half-written tag_quality stanza still fails.
func TestFinalizeConfig_DegradedAuditModeWithoutJudge(t *testing.T) {
	t.Run("audit-only with no endpoint loads", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagAudit.Enabled = true

		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v, want the degraded mode to load", err)
		}
	})

	t.Run("maintenance-only with no endpoint loads", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagMaintenance.Enabled = true

		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v, want the degraded mode to load", err)
		}
	})

	t.Run("audit-only with a half-written quality stanza is rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagAudit.Enabled = true
		cfg.Enricher.TagQuality.Llm.Provider = "test"

		err := finalizeConfig(cfg, t.TempDir())
		if err == nil {
			t.Fatal("expected an error for a half-written quality stanza")
		}
		if want := "enricher.tag_quality.llm requires both provider and model"; !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the half-written stanza %q", err, want)
		}
	})
}

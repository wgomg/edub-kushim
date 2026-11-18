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

// TestJudgedTimeoutDefaults pins the judged sections' timeout defaults: 5 s for the adjudicator and
// the lexical gate, 10 s for the quality judge, and the analyzer's 120 s untouched. 0 disables the
// deadline; there is no inheritance.
func TestJudgedTimeoutDefaults(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())

	if got := cfg.Enricher.ContentAnalyzer.Timeout; got != 120 {
		t.Errorf("content analyzer timeout = %d, want 120", got)
	}
	if got := cfg.Enricher.TagAdjudicator.Timeout; got != 5 {
		t.Errorf("tag adjudicator timeout = %d, want 5", got)
	}
	if got := cfg.Enricher.TagLexicalGate.Timeout; got != 5 {
		t.Errorf("tag lexical gate timeout = %d, want 5", got)
	}
	if got := cfg.Enricher.TagQuality.Timeout; got != 10 {
		t.Errorf("tag quality timeout = %d, want 10", got)
	}

	cfg.Enricher.TagAdjudicator.Timeout = 0
	if got := cfg.Enricher.TagAdjudicator.Timeout; got != 0 {
		t.Errorf("explicit timeout = %d, want 0 to stay 0 (no inheritance)", got)
	}
}

// TestJudgedRetryDefaultsAndBounds pins max_retries' default of 3 on all four sections and the
// 0–10 load-time bounds.
func TestJudgedRetryDefaultsAndBounds(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if got := cfg.Enricher.ContentAnalyzer.MaxRetries; got != 3 {
		t.Errorf("contentanalyzer.max_retries = %d, want 3", got)
	}
	for name, got := range map[string]int{
		"tag_adjudicator":  cfg.Enricher.TagAdjudicator.MaxRetries,
		"tag_lexical_gate": cfg.Enricher.TagLexicalGate.MaxRetries,
		"tag_quality":      cfg.Enricher.TagQuality.MaxRetries,
	} {
		if got != 3 {
			t.Errorf("%s.max_retries = %d, want 3", name, got)
		}
	}

	bad := DefaultConfig(t.TempDir())
	bad.Enricher.TagAdjudicator.MaxRetries = 11
	if err := finalizeConfig(bad, t.TempDir()); err == nil {
		t.Error("finalizeConfig accepted max_retries = 11")
	}

	bad = DefaultConfig(t.TempDir())
	bad.Enricher.ContentAnalyzer.MaxRetries = -1
	if err := finalizeConfig(bad, t.TempDir()); err == nil {
		t.Error("finalizeConfig accepted max_retries = -1")
	}
}

// TestJudgedLimitsValidation pins each section's explicit timeout and max_retries bounds at load:
// a negative timeout and an out-of-range retry count fail, while an explicit 0 stays 0.
func TestJudgedLimitsValidation(t *testing.T) {
	mutate := func(section string, timeout, retries int) *Config {
		bad := DefaultConfig(t.TempDir())
		apply := map[string]func(int, int){
			"enricher.tag_adjudicator":  func(to, re int) { bad.Enricher.TagAdjudicator.Timeout, bad.Enricher.TagAdjudicator.MaxRetries = to, re },
			"enricher.tag_lexical_gate": func(to, re int) { bad.Enricher.TagLexicalGate.Timeout, bad.Enricher.TagLexicalGate.MaxRetries = to, re },
			"enricher.tag_quality":      func(to, re int) { bad.Enricher.TagQuality.Timeout, bad.Enricher.TagQuality.MaxRetries = to, re },
		}
		apply[section](timeout, retries)
		return bad
	}

	rejections := []struct {
		name    string
		section string
		timeout int
		retries int
		want    string
	}{
		{"adjudicator negative timeout", "enricher.tag_adjudicator", -1, 3, "enricher.tag_adjudicator.timeout must be >= 0"},
		{"lexical gate negative timeout", "enricher.tag_lexical_gate", -1, 3, "enricher.tag_lexical_gate.timeout must be >= 0"},
		{"quality negative timeout", "enricher.tag_quality", -1, 3, "enricher.tag_quality.timeout must be >= 0"},
		{"adjudicator retries above cap", "enricher.tag_adjudicator", 5, 11, "enricher.tag_adjudicator.max_retries must be between 0 and 10"},
		{"lexical gate retries above cap", "enricher.tag_lexical_gate", 5, 11, "enricher.tag_lexical_gate.max_retries must be between 0 and 10"},
		{"quality retries above cap", "enricher.tag_quality", 10, 11, "enricher.tag_quality.max_retries must be between 0 and 10"},
	}
	for _, tt := range rejections {
		t.Run(tt.name, func(t *testing.T) {
			err := finalizeConfig(mutate(tt.section, tt.timeout, tt.retries), t.TempDir())
			if err == nil {
				t.Fatalf("finalizeConfig accepted %s (timeout=%d, max_retries=%d)", tt.section, tt.timeout, tt.retries)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tt.want)
			}
		})
	}

	t.Run("explicit zeroes load", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagLexicalGate.Timeout = 0
		cfg.Enricher.TagLexicalGate.MaxRetries = 0
		cfg.Enricher.TagQuality.MaxRetries = 0
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Errorf("finalizeConfig rejected explicit zeroes: %v", err)
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

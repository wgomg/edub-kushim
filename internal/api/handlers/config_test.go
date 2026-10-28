package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
)

func TestConfigResponseFrom_LoggingDefaults(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")

	resp := ConfigResponseFrom(cfg)

	if resp.App.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", resp.App.LogLevel, "info")
	}
	if resp.App.Logging.MaxSize != 100 {
		t.Errorf("Logging.MaxSize = %d, want 100", resp.App.Logging.MaxSize)
	}
	if resp.App.Logging.MaxBackups != 7 {
		t.Errorf("Logging.MaxBackups = %d, want 7", resp.App.Logging.MaxBackups)
	}
	if resp.App.Logging.MaxAge != 30 {
		t.Errorf("Logging.MaxAge = %d, want 30", resp.App.Logging.MaxAge)
	}
	if !resp.App.Logging.Compress {
		t.Error("Logging.Compress should be true by default")
	}
}

func TestConfigResponseFrom_LoggingCustomValues(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.App.LogLevel = "debug"
	cfg.App.Logging.MaxSize = 50
	cfg.App.Logging.MaxBackups = 3
	cfg.App.Logging.MaxAge = 7
	cfg.App.Logging.Compress = false

	resp := ConfigResponseFrom(cfg)

	if resp.App.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", resp.App.LogLevel, "debug")
	}
	if resp.App.Logging.MaxSize != 50 {
		t.Errorf("Logging.MaxSize = %d, want 50", resp.App.Logging.MaxSize)
	}
	if resp.App.Logging.MaxBackups != 3 {
		t.Errorf("Logging.MaxBackups = %d, want 3", resp.App.Logging.MaxBackups)
	}
	if resp.App.Logging.MaxAge != 7 {
		t.Errorf("Logging.MaxAge = %d, want 7", resp.App.Logging.MaxAge)
	}
	if resp.App.Logging.Compress {
		t.Error("Logging.Compress should be false")
	}
}

func TestConfigResponseFrom_ReclaimMaxRetries(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Consumer.Reclaim.Enabled = true
	cfg.Consumer.Reclaim.MaxRetries = 5

	resp := ConfigResponseFrom(cfg)

	if resp.Consumer.Reclaim.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", resp.Consumer.Reclaim.MaxRetries)
	}
	if !resp.Consumer.Reclaim.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestConfigResponseFrom_PromptTemplate(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")

	resp := ConfigResponseFrom(cfg)
	if resp.Enricher.ContentAnalyzer.PromptTemplate != "" {
		t.Errorf("default PromptTemplate = %q, want empty", resp.Enricher.ContentAnalyzer.PromptTemplate)
	}

	cfg.Enricher.ContentAnalyzer.PromptTemplate = "custom {{.Text}} template"
	resp = ConfigResponseFrom(cfg)
	if resp.Enricher.ContentAnalyzer.PromptTemplate != "custom {{.Text}} template" {
		t.Errorf("PromptTemplate = %q, want %q", resp.Enricher.ContentAnalyzer.PromptTemplate, "custom {{.Text}} template")
	}
}

func TestConfigResponseFrom_MaxBatchDelete(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	resp := ConfigResponseFrom(cfg)

	if resp.Server.MaxBatchDelete != 50 {
		t.Errorf("default MaxBatchDelete = %d, want 50", resp.Server.MaxBatchDelete)
	}

	cfg.Srv.MaxBatchDelete = 100
	resp = ConfigResponseFrom(cfg)
	if resp.Server.MaxBatchDelete != 100 {
		t.Errorf("MaxBatchDelete = %d, want 100", resp.Server.MaxBatchDelete)
	}
}

func TestConfigResponseFrom_DocTypeRefinement(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	resp := ConfigResponseFrom(cfg)

	if !resp.Enricher.ContentAnalyzer.DocTypeRefinement.Enabled {
		t.Error("default DocTypeRefinement.Enabled should be true")
	}
	if resp.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords != 600 {
		t.Errorf("default HeadWords = %d, want 600", resp.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords)
	}
	if resp.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords != 400 {
		t.Errorf("default TailWords = %d, want 400", resp.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords)
	}

	cfg.Enricher.ContentAnalyzer.DocTypeRefinement.Enabled = false
	cfg.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords = 300
	cfg.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords = 200
	resp = ConfigResponseFrom(cfg)

	if resp.Enricher.ContentAnalyzer.DocTypeRefinement.Enabled {
		t.Error("DocTypeRefinement.Enabled should be false")
	}
	if resp.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords != 300 {
		t.Errorf("HeadWords = %d, want 300", resp.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords)
	}
	if resp.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords != 200 {
		t.Errorf("TailWords = %d, want 200", resp.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords)
	}
}

func TestConfigResponseFrom_PauseOnCreditError(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	resp := ConfigResponseFrom(cfg)

	if !resp.Enricher.ContentAnalyzer.PauseOnCreditError {
		t.Error("default PauseOnCreditError should be true")
	}

	cfg.Enricher.ContentAnalyzer.PauseOnCreditError = false
	resp = ConfigResponseFrom(cfg)
	if resp.Enricher.ContentAnalyzer.PauseOnCreditError {
		t.Error("PauseOnCreditError should be false after setting")
	}
}

// TestConfigResponseFrom_TagMatcherAndAuditSurfaced pins the fields the settings UI reads back
// from GET /config: the matcher suggestion count and the whole tag_audit section, defaults first
// and overridden values second.
func TestConfigResponseFrom_TagMatcherAndAuditSurfaced(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	resp := ConfigResponseFrom(cfg)

	if resp.Enricher.TagMatcher.TopN != 15 {
		t.Errorf("default TopN = %d, want 15", resp.Enricher.TagMatcher.TopN)
	}
	if resp.Enricher.TagAudit.Enabled {
		t.Error("default TagAudit.Enabled should be false")
	}
	if resp.Enricher.TagAudit.Interval != 7 {
		t.Errorf("default TagAudit.Interval = %v, want 7", resp.Enricher.TagAudit.Interval)
	}
	if resp.Enricher.TagAudit.Time != "04:00" {
		t.Errorf("default TagAudit.Time = %q, want 04:00", resp.Enricher.TagAudit.Time)
	}
	if resp.Enricher.TagAudit.SampleSize != 150 {
		t.Errorf("default TagAudit.SampleSize = %d, want 150", resp.Enricher.TagAudit.SampleSize)
	}

	cfg.Enricher.TagMatcher.TopN = 42
	cfg.Enricher.TagAudit.Enabled = true
	cfg.Enricher.TagAudit.Interval = 3
	cfg.Enricher.TagAudit.Time = "05:15"
	cfg.Enricher.TagAudit.SampleSize = 300
	resp = ConfigResponseFrom(cfg)

	if resp.Enricher.TagMatcher.TopN != 42 {
		t.Errorf("TopN = %d, want 42 after setting", resp.Enricher.TagMatcher.TopN)
	}
	if !resp.Enricher.TagAudit.Enabled ||
		resp.Enricher.TagAudit.Interval != 3 ||
		resp.Enricher.TagAudit.Time != "05:15" ||
		resp.Enricher.TagAudit.SampleSize != 300 {
		t.Errorf("TagAudit = %+v, want the overridden values", resp.Enricher.TagAudit)
	}
}

// TestConfigResponseFrom_TagMaintenanceSurfaced pins the maintenance-routine section the settings UI reads
// back from GET /config: defaults first and overridden values second, so the card round-trips the
// operator's schedule instead of re-persisting the defaults on every save.
func TestConfigResponseFrom_TagMaintenanceSurfaced(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	resp := ConfigResponseFrom(cfg)

	if resp.Enricher.TagMaintenance.Enabled {
		t.Error("default TagMaintenance.Enabled should be false")
	}
	if resp.Enricher.TagMaintenance.Interval != 7 {
		t.Errorf("default TagMaintenance.Interval = %v, want 7", resp.Enricher.TagMaintenance.Interval)
	}
	if resp.Enricher.TagMaintenance.Time != "02:00" {
		t.Errorf("default TagMaintenance.Time = %q, want 02:00", resp.Enricher.TagMaintenance.Time)
	}

	cfg.Enricher.TagMaintenance.Enabled = true
	cfg.Enricher.TagMaintenance.Interval = 3
	cfg.Enricher.TagMaintenance.Time = "05:15"
	resp = ConfigResponseFrom(cfg)

	if !resp.Enricher.TagMaintenance.Enabled ||
		resp.Enricher.TagMaintenance.Interval != 3 ||
		resp.Enricher.TagMaintenance.Time != "05:15" {
		t.Errorf("TagMaintenance = %+v, want the overridden values", resp.Enricher.TagMaintenance)
	}
}

// TestConfigResponseFrom_OmitsMaxTags pins the removed max_tags field: the settings payload
// must never carry it again, or the web UI would show a knob the backend no longer reads.
func TestConfigResponseFrom_OmitsMaxTags(t *testing.T) {
	data, err := json.Marshal(ConfigResponseFrom(config.DefaultConfig("/tmp/test")).Enricher.ContentAnalyzer)
	if err != nil {
		t.Fatalf("marshal ContentAnalyzerResponse: %v", err)
	}
	if strings.Contains(string(data), "max_tags") {
		t.Errorf("content analyzer response carries the removed max_tags field: %s", data)
	}
}

func TestConfigResponseFrom_RequestDelay(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	resp := ConfigResponseFrom(cfg)

	if resp.Enricher.ContentAnalyzer.Llm.RequestDelay != 1 {
		t.Errorf("default RequestDelay = %v, want 1", resp.Enricher.ContentAnalyzer.Llm.RequestDelay)
	}

	cfg.Enricher.ContentAnalyzer.Llm.RequestDelay = 2.5
	resp = ConfigResponseFrom(cfg)
	if resp.Enricher.ContentAnalyzer.Llm.RequestDelay != 2.5 {
		t.Errorf("RequestDelay = %v, want 2.5", resp.Enricher.ContentAnalyzer.Llm.RequestDelay)
	}
}

func TestConfigResponseFrom_Fallback(t *testing.T) {
	t.Run("omits fallbacks when empty", func(t *testing.T) {
		cfg := config.DefaultConfig("/tmp/test")
		cfg.Enricher.ContentAnalyzer.Fallbacks = nil

		resp := ConfigResponseFrom(cfg)

		if len(resp.Enricher.ContentAnalyzer.Fallbacks) != 0 {
			t.Errorf("Fallbacks = %+v, want empty", resp.Enricher.ContentAnalyzer.Fallbacks)
		}
	})

	t.Run("maps enabled fallback with all llm fields", func(t *testing.T) {
		cfg := config.DefaultConfig("/tmp/test")
		cfg.Enricher.ContentAnalyzer.Fallbacks = []config.FallbackConfig{
			{
				Enabled: true,
				Llm: config.LlmConfig{
					Adapter:      "openai-compatible",
					Provider:     "deepseek",
					Model:        "deepseek-chat",
					Token:        "sk-fb-123",
					Temperature:  0.7,
					RequestDelay: 2,
					Reasoning:    true,
				},
			},
		}

		resp := ConfigResponseFrom(cfg)

		if len(resp.Enricher.ContentAnalyzer.Fallbacks) != 1 {
			t.Fatalf("Fallbacks len = %d, want 1", len(resp.Enricher.ContentAnalyzer.Fallbacks))
		}
		fb := &resp.Enricher.ContentAnalyzer.Fallbacks[0]
		if !fb.Enabled {
			t.Error("Enabled should be true")
		}
		if fb.Llm.Adapter != "openai-compatible" {
			t.Errorf("Llm.Adapter = %q", fb.Llm.Adapter)
		}
		if fb.Llm.Provider != "deepseek" {
			t.Errorf("Llm.Provider = %q", fb.Llm.Provider)
		}
		if fb.Llm.Model != "deepseek-chat" {
			t.Errorf("Llm.Model = %q", fb.Llm.Model)
		}
		if fb.Llm.Token != "sk-fb-123" {
			t.Errorf("Llm.Token = %q", fb.Llm.Token)
		}
		if fb.Llm.Temperature != 0.7 {
			t.Errorf("Llm.Temperature = %v", fb.Llm.Temperature)
		}
		if fb.Llm.RequestDelay != 2 {
			t.Errorf("Llm.RequestDelay = %v", fb.Llm.RequestDelay)
		}
		if !fb.Llm.Reasoning {
			t.Error("Llm.Reasoning should be true")
		}
	})
}

func TestConfigResponseFrom_ConsumerFields(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Consumer.SupportedFiles = []string{".pdf", ".tiff"}
	cfg.Consumer.Converter.Enabled = true
	cfg.Consumer.Converter.Binary = "/usr/bin/soffice"
	cfg.Consumer.Converter.Timeout = 120

	resp := ConfigResponseFrom(cfg)

	if len(resp.Consumer.SupportedFiles) != 2 || resp.Consumer.SupportedFiles[0] != ".pdf" || resp.Consumer.SupportedFiles[1] != ".tiff" {
		t.Errorf("SupportedFiles = %v, want [.pdf .tiff]", resp.Consumer.SupportedFiles)
	}
	if !resp.Consumer.Converter.Enabled {
		t.Error("Converter.Enabled should be true")
	}
	if resp.Consumer.Converter.Binary != "/usr/bin/soffice" {
		t.Errorf("Converter.Binary = %q", resp.Consumer.Converter.Binary)
	}
	if resp.Consumer.Converter.Timeout != 120 {
		t.Errorf("Converter.Timeout = %d, want 120", resp.Consumer.Converter.Timeout)
	}
}

func TestConfigResponseFrom_AvailableFileTypes(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	resp := ConfigResponseFrom(cfg)

	if len(resp.AvailableFileTypes) != 6 {
		t.Fatalf("AvailableFileTypes has %d entries, want 6", len(resp.AvailableFileTypes))
	}

	pdf := resp.AvailableFileTypes[0]
	if pdf.MimeType != "application/pdf" {
		t.Errorf("PDF MimeType = %q", pdf.MimeType)
	}
	if !pdf.Required {
		t.Error("PDF should be required")
	}
	if len(pdf.Extensions) != 1 || pdf.Extensions[0] != ".pdf" {
		t.Errorf("PDF Extensions = %v", pdf.Extensions)
	}

	tiff := resp.AvailableFileTypes[3]
	if tiff.MimeType != "image/tiff" {
		t.Errorf("TIFF MimeType = %q", tiff.MimeType)
	}
	if tiff.Required {
		t.Error("TIFF should not be required")
	}
	if len(tiff.Extensions) != 2 || tiff.Extensions[0] != ".tiff" || tiff.Extensions[1] != ".tif" {
		t.Errorf("TIFF Extensions = %v, want [.tiff .tif]", tiff.Extensions)
	}

	jpeg := resp.AvailableFileTypes[4]
	if len(jpeg.Extensions) != 2 || jpeg.Extensions[0] != ".jpg" || jpeg.Extensions[1] != ".jpeg" {
		t.Errorf("JPEG Extensions = %v, want [.jpg .jpeg]", jpeg.Extensions)
	}
}

func TestConfigResponseFrom_TagMeasure(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Enricher.TagMeasure.Enabled = true
	cfg.Enricher.TagMeasure.Interval = 3
	cfg.Enricher.TagMeasure.Time = "04:30"
	cfg.Enricher.TagMeasure.AfterEnrichmentBatch = true

	resp := ConfigResponseFrom(cfg)

	if !resp.Enricher.TagMeasure.Enabled {
		t.Error("TagMeasure.Enabled = false, want true")
	}
	if resp.Enricher.TagMeasure.Interval != 3 {
		t.Errorf("TagMeasure.Interval = %v, want 3", resp.Enricher.TagMeasure.Interval)
	}
	if resp.Enricher.TagMeasure.Time != "04:30" {
		t.Errorf("TagMeasure.Time = %q, want 04:30", resp.Enricher.TagMeasure.Time)
	}
	if !resp.Enricher.TagMeasure.AfterEnrichmentBatch {
		t.Error("TagMeasure.AfterEnrichmentBatch = false, want true")
	}
}

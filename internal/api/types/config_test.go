package types

import (
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

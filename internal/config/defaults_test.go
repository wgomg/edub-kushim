package config

import "testing"

func TestDefaultConfig_PdfOptimizerTimeoutDisabledByDefault(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Consumer.PdfOptimizer.Timeout != 0 {
		t.Errorf("PdfOptimizer.Timeout = %d, want 0 (disabled by default)", cfg.Consumer.PdfOptimizer.Timeout)
	}
}

func TestDefaultConfig_TextExtractorAndOCRTimeoutsUnchanged(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Consumer.TextExtractor.Timeout != 120 {
		t.Errorf("TextExtractor.Timeout = %d, want 120", cfg.Consumer.TextExtractor.Timeout)
	}
	if cfg.Consumer.OCR.Timeout != 120 {
		t.Errorf("OCR.Timeout = %d, want 120", cfg.Consumer.OCR.Timeout)
	}
}

func TestDefaultConfig_PauseOnCreditError(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if !cfg.Enricher.ContentAnalyzer.PauseOnCreditError {
		t.Error("PauseOnCreditError should default to true")
	}
}

func TestDefaultConfig_ConverterDefaults(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Consumer.Converter.Enabled {
		t.Error("Converter.Enabled should default to false")
	}
	if cfg.Consumer.Converter.Binary != "libreoffice" {
		t.Errorf("Converter.Binary = %q, want %q", cfg.Consumer.Converter.Binary, "libreoffice")
	}
	if cfg.Consumer.Converter.Timeout != 300 {
		t.Errorf("Converter.Timeout = %d, want 300", cfg.Consumer.Converter.Timeout)
	}
}

func TestDefaultConfig_SupportedFilesExcludesDocxOdt(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	for _, ext := range cfg.Consumer.SupportedFiles {
		if ext == ".docx" || ext == ".odt" {
			t.Errorf("SupportedFiles should not include %s by default (converter is opt-in)", ext)
		}
	}
}

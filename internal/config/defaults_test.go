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

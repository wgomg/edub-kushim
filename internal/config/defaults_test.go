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

func TestDefaultConfig_RequestDelay(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Enricher.ContentAnalyzer.Llm.RequestDelay != 1 {
		t.Errorf("RequestDelay = %v, want 1", cfg.Enricher.ContentAnalyzer.Llm.RequestDelay)
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

func TestDefaultIdentityCandidateSimilarity(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		{model: "bge-m3", want: 0.80},
		{model: "all-mpnet-base-v2", want: 0.75},
		{model: "all-MiniLM-L6-v2", want: 0.70},
		{model: "unknown-model", want: 0.75},
	}
	for _, tt := range tests {
		got := defaultIdentityCandidateSimilarity(tt.model)
		if got != tt.want {
			t.Errorf("defaultIdentityCandidateSimilarity(%q) = %v, want %v", tt.model, got, tt.want)
		}
		if got <= defaultMinSimilarity(tt.model) {
			t.Errorf("defaultIdentityCandidateSimilarity(%q) = %v must stay above the document-to-tag threshold %v",
				tt.model, got, defaultMinSimilarity(tt.model))
		}
	}
}

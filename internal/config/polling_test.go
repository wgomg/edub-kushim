package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHHMM_Valid(t *testing.T) {
	tests := []struct {
		input    string
		allowEnd bool
		want     int
	}{
		{"00:00", false, 0},
		{"09:30", false, 570},
		{"12:00", false, 720},
		{"23:59", false, 1439},
		{"24:00", true, 1440},
		{"01:05", false, 65},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseHHMM(tt.input, tt.allowEnd)
			if err != nil {
				t.Fatalf("parseHHMM(%q, %v) unexpected error: %v", tt.input, tt.allowEnd, err)
			}
			if got != tt.want {
				t.Errorf("parseHHMM(%q, %v) = %d, want %d", tt.input, tt.allowEnd, got, tt.want)
			}
		})
	}
}

func TestParseHHMM_Invalid(t *testing.T) {
	tests := []struct {
		input    string
		allowEnd bool
	}{
		{"25:00", false},
		{"12:60", false},
		{"abc", false},
		{"", false},
		{"24:00", false},
		{"24:01", true},
		{"9:30", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := parseHHMM(tt.input, tt.allowEnd)
			if err == nil {
				t.Fatalf("parseHHMM(%q, %v) expected error, got nil", tt.input, tt.allowEnd)
			}
		})
	}
}

func TestValidatePollingWindows_Valid(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"no key", map[string]any{}},
		{"empty array", map[string]any{"consumer.polling.windows": []any{}}},
		{"single window", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "06:00"},
			},
		}},
		{"multiple windows", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "06:00"},
				map[string]any{"start": "14:00", "end": "16:00"},
			},
		}},
		{"24:00 end", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "22:00", "end": "24:00"},
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePollingWindows(tt.body); err != nil {
				t.Fatalf("ValidatePollingWindows() unexpected error: %v", err)
			}
		})
	}
}

func TestValidatePollingWindows_Invalid(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"nil value", map[string]any{"consumer.polling.windows": nil}},
		{"wrong type", map[string]any{"consumer.polling.windows": "not-array"}},
		{"non-object entry", map[string]any{
			"consumer.polling.windows": []any{"bad"},
		}},
		{"missing start", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"end": "06:00"},
			},
		}},
		{"missing end", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00"},
			},
		}},
		{"invalid start format", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "25:00", "end": "06:00"},
			},
		}},
		{"invalid end format", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "25:00"},
			},
		}},
		{"end is 00:00", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "00:00"},
			},
		}},
		{"start equals end", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "02:00", "end": "02:00"},
			},
		}},
		{"start after end", map[string]any{
			"consumer.polling.windows": []any{
				map[string]any{"start": "06:00", "end": "02:00"},
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePollingWindows(tt.body); err == nil {
				t.Fatal("ValidatePollingWindows() expected error, got nil")
			}
		})
	}
}

func TestIsWithinActiveWindows_EmptyWindows(t *testing.T) {
	if !IsWithinActiveWindows(nil) {
		t.Error("IsWithinActiveWindows(nil) = false, want true")
	}
	if !IsWithinActiveWindows([]PollingWindow{}) {
		t.Error("IsWithinActiveWindows([]) = false, want true")
	}
}

func TestDefaultConfig_OcrWorkers(t *testing.T) {
	configDir := t.TempDir()
	cfg := DefaultConfig(configDir)

	if cfg.Consumer.OCR.OcrWorkers != 0 {
		t.Errorf("default OcrWorkers = %d, want 0", cfg.Consumer.OCR.OcrWorkers)
	}

	cfg.Consumer.OCR.OcrWorkers = 4
	if err := finalizeConfig(cfg, configDir); err != nil {
		t.Fatalf("finalizeConfig: %v", err)
	}
	if cfg.Consumer.OCR.OcrWorkers != 4 {
		t.Errorf("OcrWorkers after finalize = %d, want 4", cfg.Consumer.OCR.OcrWorkers)
	}
}

func TestLoad_AppliesOcrWorkers(t *testing.T) {
	configDir := t.TempDir()
	yaml := `consumer:
  ocr:
    languages:
      - eng
    ocr_workers: 8
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Consumer.OCR.OcrWorkers != 8 {
		t.Errorf("OcrWorkers after load = %d, want 8", cfg.Consumer.OCR.OcrWorkers)
	}
}

func TestFinalizeConfig_ReclaimMaxRetries(t *testing.T) {
	configDir := t.TempDir()

	t.Run("rejects max_retries zero when reclaim enabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Consumer.Reclaim.Enabled = true
		cfg.Consumer.Reclaim.MaxRetries = 0

		err := finalizeConfig(cfg, configDir)
		if err == nil {
			t.Fatal("expected error for MaxRetries=0 with reclaim enabled")
		}
	})

	t.Run("rejects negative max_retries when reclaim enabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Consumer.Reclaim.Enabled = true
		cfg.Consumer.Reclaim.MaxRetries = -1

		err := finalizeConfig(cfg, configDir)
		if err == nil {
			t.Fatal("expected error for MaxRetries=-1 with reclaim enabled")
		}
	})

	t.Run("allows zero max_retries when reclaim disabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Consumer.Reclaim.Enabled = false
		cfg.Consumer.Reclaim.MaxRetries = 0

		err := finalizeConfig(cfg, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("accepts positive max_retries when reclaim enabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Consumer.Reclaim.Enabled = true
		cfg.Consumer.Reclaim.MaxRetries = 5

		err := finalizeConfig(cfg, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestFinalizeConfig_StaleTaskAfterMinimum(t *testing.T) {
	configDir := t.TempDir()

	t.Run("rejects stale_task_after below 60 when reclaim enabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Consumer.Reclaim.Enabled = true
		cfg.Consumer.Reclaim.MaxRetries = 3
		cfg.Consumer.Reclaim.StaleTaskAfter = 30

		err := finalizeConfig(cfg, configDir)
		if err == nil {
			t.Fatal("expected error for StaleTaskAfter=30 with reclaim enabled")
		}
	})

	t.Run("rejects stale_task_after zero when reclaim enabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Consumer.Reclaim.Enabled = true
		cfg.Consumer.Reclaim.MaxRetries = 3
		cfg.Consumer.Reclaim.StaleTaskAfter = 0

		err := finalizeConfig(cfg, configDir)
		if err == nil {
			t.Fatal("expected error for StaleTaskAfter=0 with reclaim enabled")
		}
	})

	t.Run("allows stale_task_after 60 when reclaim enabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Consumer.Reclaim.Enabled = true
		cfg.Consumer.Reclaim.MaxRetries = 3
		cfg.Consumer.Reclaim.StaleTaskAfter = 60

		err := finalizeConfig(cfg, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("allows low stale_task_after when reclaim disabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Consumer.Reclaim.Enabled = false
		cfg.Consumer.Reclaim.StaleTaskAfter = 5

		err := finalizeConfig(cfg, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestFinalizeConfig_MaxBatchDeleteMinimum(t *testing.T) {
	configDir := t.TempDir()

	t.Run("rejects zero max_batch_delete", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Srv.MaxBatchDelete = 0

		err := finalizeConfig(cfg, configDir)
		if err == nil {
			t.Fatal("expected error for MaxBatchDelete=0")
		}
	})

	t.Run("rejects negative max_batch_delete", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Srv.MaxBatchDelete = -1

		err := finalizeConfig(cfg, configDir)
		if err == nil {
			t.Fatal("expected error for MaxBatchDelete=-1")
		}
	})

	t.Run("accepts max_batch_delete of 1", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Srv.MaxBatchDelete = 1

		err := finalizeConfig(cfg, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestFinalizeConfig_DocTypeRefinementNonNegative(t *testing.T) {
	configDir := t.TempDir()

	t.Run("rejects negative head_words", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords = -1

		err := finalizeConfig(cfg, configDir)
		if err == nil {
			t.Fatal("expected error for HeadWords=-1")
		}
	})

	t.Run("rejects negative tail_words", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords = -1

		err := finalizeConfig(cfg, configDir)
		if err == nil {
			t.Fatal("expected error for TailWords=-1")
		}
	})

	t.Run("accepts zero head_words and tail_words", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords = 0
		cfg.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords = 0

		err := finalizeConfig(cfg, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

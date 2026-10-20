package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestFinalizeConfig_RequestDelayBounds(t *testing.T) {
	tests := []struct {
		name    string
		delay   float64
		wantErr bool
	}{
		{"negative rejected", -1, true},
		{"zero allowed (disabled)", 0, false},
		{"default allowed", 1, false},
		{"max allowed", 60, false},
		{"over max rejected", 61, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()
			cfg := DefaultConfig(configDir)
			cfg.Enricher.ContentAnalyzer.Llm.RequestDelay = tt.delay

			err := finalizeConfig(cfg, configDir)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for request_delay=%v", tt.delay)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for request_delay=%v: %v", tt.delay, err)
			}
		})
	}
}

func TestFinalizeConfig_FallbackValidation(t *testing.T) {
	configDir := t.TempDir()

	validFB := func() []FallbackConfig {
		return []FallbackConfig{
			{
				Enabled: true,
				Llm: LlmConfig{
					Adapter:      "openai-compatible",
					Provider:     "deepseek",
					Model:        "deepseek-chat",
					RequestDelay: 1,
				},
			},
		}
	}

	t.Run("rejects enabled fallback missing adapter", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.Fallbacks = validFB()
		cfg.Enricher.ContentAnalyzer.Fallbacks[0].Llm.Adapter = ""

		err := finalizeConfig(cfg, configDir)
		if err == nil || !strings.Contains(err.Error(), "fallbacks[0].llm.adapter is required") {
			t.Errorf("expected missing-adapter error, got %v", err)
		}
	})

	t.Run("rejects enabled fallback missing provider", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.Fallbacks = validFB()
		cfg.Enricher.ContentAnalyzer.Fallbacks[0].Llm.Provider = ""

		err := finalizeConfig(cfg, configDir)
		if err == nil || !strings.Contains(err.Error(), "fallbacks[0].llm.provider is required") {
			t.Errorf("expected missing-provider error, got %v", err)
		}
	})

	t.Run("rejects enabled fallback missing model", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.Fallbacks = validFB()
		cfg.Enricher.ContentAnalyzer.Fallbacks[0].Llm.Model = ""

		err := finalizeConfig(cfg, configDir)
		if err == nil || !strings.Contains(err.Error(), "fallbacks[0].llm.model is required") {
			t.Errorf("expected missing-model error, got %v", err)
		}
	})

	t.Run("rejects enabled fallback with negative request_delay", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.Fallbacks = validFB()
		cfg.Enricher.ContentAnalyzer.Fallbacks[0].Llm.RequestDelay = -1

		err := finalizeConfig(cfg, configDir)
		if err == nil || !strings.Contains(err.Error(), "fallbacks[0].llm.request_delay must be between") {
			t.Errorf("expected request_delay bounds error, got %v", err)
		}
	})

	t.Run("accepts fully-specified enabled fallback", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.Fallbacks = validFB()

		if err := finalizeConfig(cfg, configDir); err != nil {
			t.Errorf("unexpected error for valid fallback: %v", err)
		}
	})

	t.Run("skips validation when fallbacks is empty", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.Fallbacks = nil

		if err := finalizeConfig(cfg, configDir); err != nil {
			t.Errorf("unexpected error when fallbacks is empty: %v", err)
		}
	})

	t.Run("skips validation when fallback is disabled", func(t *testing.T) {
		cfg := DefaultConfig(configDir)
		cfg.Enricher.ContentAnalyzer.Fallbacks = []FallbackConfig{
			{
				Enabled: false,
				Llm:     LlmConfig{}, // intentionally empty
			},
		}

		if err := finalizeConfig(cfg, configDir); err != nil {
			t.Errorf("unexpected error for disabled fallback with empty llm: %v", err)
		}
	})
}

func TestFinalizeConfig_BackupValidation(t *testing.T) {
	t.Run("flat fields auto-convert to a single full schedule", func(t *testing.T) {
		configDir := t.TempDir()
		cfg := DefaultConfig(configDir)
		cfg.Backup.Enabled = true
		cfg.Backup.Interval = 3
		cfg.Backup.Time = "04:00"
		cfg.Backup.Keep = 5

		if err := finalizeConfig(cfg, configDir); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if got := len(cfg.Backup.Schedules); got != 1 {
			t.Fatalf("Schedules len = %d, want 1", got)
		}
		s := cfg.Backup.Schedules[0]
		if s.Mode != "full" || s.Interval != 3 || s.Time != "04:00" || s.Keep != 5 {
			t.Errorf("auto-converted schedule = %+v, want full/3/04:00/5", s)
		}
	})

	t.Run("flat + schedules rejected", func(t *testing.T) {
		configDir := t.TempDir()
		cfg := DefaultConfig(configDir)
		cfg.Backup.Enabled = true
		cfg.Backup.Interval = 1
		cfg.Backup.Schedules = []BackupSchedule{{Mode: "full", Interval: 1, Time: "02:00", Keep: 1}}

		err := finalizeConfig(cfg, configDir)
		if err == nil || !strings.Contains(err.Error(), "remove deprecated flat") {
			t.Errorf("expected flat+schedules conflict error, got %v", err)
		}
	})

	t.Run("enabled with no schedule and no flat fields is rejected", func(t *testing.T) {
		configDir := t.TempDir()
		cfg := DefaultConfig(configDir)
		cfg.Backup.Enabled = true

		err := finalizeConfig(cfg, configDir)
		if err == nil || !strings.Contains(err.Error(), "at least one backup schedule") {
			t.Errorf("expected at-least-one-schedule error, got %v", err)
		}
	})

	t.Run("invalid mode in schedule rejected", func(t *testing.T) {
		configDir := t.TempDir()
		cfg := DefaultConfig(configDir)
		cfg.Backup.Enabled = true
		cfg.Backup.Schedules = []BackupSchedule{{Mode: "bogus", Interval: 1, Time: "02:00", Keep: 1}}

		err := finalizeConfig(cfg, configDir)
		if err == nil || !strings.Contains(err.Error(), "mode must be one of") {
			t.Errorf("expected invalid-mode error, got %v", err)
		}
	})

	t.Run("duplicate modes rejected", func(t *testing.T) {
		configDir := t.TempDir()
		cfg := DefaultConfig(configDir)
		cfg.Backup.Enabled = true
		cfg.Backup.Schedules = []BackupSchedule{
			{Mode: "full", Interval: 1, Time: "02:00", Keep: 1},
			{Mode: "full", Interval: 2, Time: "03:00", Keep: 2},
		}

		err := finalizeConfig(cfg, configDir)
		if err == nil || !strings.Contains(err.Error(), "duplicate mode") {
			t.Errorf("expected duplicate-mode error, got %v", err)
		}
	})
}

func TestLoad_BackupFlatAutoConverts(t *testing.T) {
	configDir := t.TempDir()
	yaml := `consumer:
  ocr:
    languages:
      - eng
backup:
  enabled: true
  interval: 2
  time: "05:00"
  keep: 3
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(cfg.Backup.Schedules); got != 1 {
		t.Fatalf("Schedules len = %d, want 1", got)
	}
	s := cfg.Backup.Schedules[0]
	if s.Mode != "full" || s.Interval != 2 || s.Time != "05:00" || s.Keep != 3 {
		t.Errorf("auto-converted schedule = %+v, want full/2/05:00/3", s)
	}
}

func TestFinalizeConfig_ThumbnailBackfillValidation(t *testing.T) {
	tests := []struct {
		name     string
		interval float64
		time     string
		wantErr  bool
	}{
		{"negative interval rejected", -0.5, "02:00", true},
		{"zero interval disables time check", 0, "not-a-time", false},
		{"valid interval and time", 1, "03:30", false},
		{"sub-daily interval valid", 0.5, "14:00", false},
		{"empty time defaults to valid", 1, "", false},
		{"invalid time rejected", 1, "25:99", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()
			cfg := DefaultConfig(configDir)
			cfg.Consumer.Thumbnail.BackfillInterval = tt.interval
			cfg.Consumer.Thumbnail.BackfillTime = tt.time

			err := finalizeConfig(cfg, configDir)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for interval=%v time=%q", tt.interval, tt.time)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for interval=%v time=%q: %v", tt.interval, tt.time, err)
			}
		})
	}
}

func TestDefaultConfig_TagReconcile(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Enricher.TagReconcile.Enabled {
		t.Error("TagReconcile.Enabled should default to false")
	}
	if !cfg.Enricher.TagReconcile.ReportOnly {
		t.Error("TagReconcile.ReportOnly should default to true (first run is structural)")
	}
	if cfg.Enricher.TagReconcile.Interval != 7 {
		t.Errorf("TagReconcile.Interval = %v, want 7", cfg.Enricher.TagReconcile.Interval)
	}
	if cfg.Enricher.TagReconcile.Time != "02:00" {
		t.Errorf("TagReconcile.Time = %q, want 02:00", cfg.Enricher.TagReconcile.Time)
	}
	if cfg.Enricher.TagReconcile.MaxRejudge != 200 {
		t.Errorf("TagReconcile.MaxRejudge = %d, want 200", cfg.Enricher.TagReconcile.MaxRejudge)
	}
	if cfg.Enricher.TagReconcile.MaxRetag != 1000 {
		t.Errorf("TagReconcile.MaxRetag = %d, want 1000", cfg.Enricher.TagReconcile.MaxRetag)
	}
	if cfg.Enricher.TagReconcile.DeadTagGraceDays != 7 {
		t.Errorf("TagReconcile.DeadTagGraceDays = %d, want 7", cfg.Enricher.TagReconcile.DeadTagGraceDays)
	}
	if cfg.Enricher.TagReconcile.ProvisionalGraceDays != 30 {
		t.Errorf("TagReconcile.ProvisionalGraceDays = %d, want 30", cfg.Enricher.TagReconcile.ProvisionalGraceDays)
	}
}

func TestFinalizeConfig_TagReconcile(t *testing.T) {
	t.Run("zero interval defaults to 7", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.Interval = 0
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.Interval != 7 {
			t.Errorf("Interval = %v, want 7", cfg.Enricher.TagReconcile.Interval)
		}
	})
	t.Run("negative interval defaults to 7", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.Interval = -1
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.Interval != 7 {
			t.Errorf("Interval = %v, want 7", cfg.Enricher.TagReconcile.Interval)
		}
	})
	t.Run("zero max_rejudge defaults to 200", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.MaxRejudge = 0
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.MaxRejudge != 200 {
			t.Errorf("MaxRejudge = %d, want 200", cfg.Enricher.TagReconcile.MaxRejudge)
		}
	})
	t.Run("zero max_retag defaults to 1000", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.MaxRetag = 0
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.MaxRetag != 1000 {
			t.Errorf("MaxRetag = %d, want 1000", cfg.Enricher.TagReconcile.MaxRetag)
		}
	})
	t.Run("zero dead_tag_grace_days defaults to 7", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.DeadTagGraceDays = 0
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.DeadTagGraceDays != 7 {
			t.Errorf("DeadTagGraceDays = %d, want 7", cfg.Enricher.TagReconcile.DeadTagGraceDays)
		}
	})
	t.Run("negative dead_tag_grace_days defaults to 7", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.DeadTagGraceDays = -1
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.DeadTagGraceDays != 7 {
			t.Errorf("DeadTagGraceDays = %d, want 7", cfg.Enricher.TagReconcile.DeadTagGraceDays)
		}
	})
	t.Run("zero provisional_grace_days defaults to 30", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.ProvisionalGraceDays = 0
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.ProvisionalGraceDays != 30 {
			t.Errorf("ProvisionalGraceDays = %d, want 30", cfg.Enricher.TagReconcile.ProvisionalGraceDays)
		}
	})
	t.Run("negative provisional_grace_days defaults to 30", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.ProvisionalGraceDays = -1
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.ProvisionalGraceDays != 30 {
			t.Errorf("ProvisionalGraceDays = %d, want 30", cfg.Enricher.TagReconcile.ProvisionalGraceDays)
		}
	})
	t.Run("empty time defaults to 02:00", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.Time = ""
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.Time != "02:00" {
			t.Errorf("Time = %q, want 02:00", cfg.Enricher.TagReconcile.Time)
		}
	})
	t.Run("invalid time rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.Time = "25:99"
		if err := finalizeConfig(cfg, t.TempDir()); err == nil {
			t.Error("expected error for time=25:99")
		}
	})
	t.Run("non-integer minute rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.Time = "12:7"
		if err := finalizeConfig(cfg, t.TempDir()); err == nil {
			t.Error("expected error for time=12:7")
		}
	})
	t.Run("explicit positive values preserved", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Enricher.TagReconcile.Interval = 3
		cfg.Enricher.TagReconcile.Time = "04:30"
		cfg.Enricher.TagReconcile.MaxRejudge = 50
		cfg.Enricher.TagReconcile.MaxRetag = 500
		if err := finalizeConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("finalizeConfig: %v", err)
		}
		if cfg.Enricher.TagReconcile.Interval != 3 || cfg.Enricher.TagReconcile.Time != "04:30" ||
			cfg.Enricher.TagReconcile.MaxRejudge != 50 || cfg.Enricher.TagReconcile.MaxRetag != 500 {
			t.Fatalf("explicit values overwritten: %+v", cfg.Enricher.TagReconcile)
		}
	})
}

// TestFinalizeConfig_ServerTimeouts pins the plain-int timeout contract: the built-in defaults
// validate, and a negative value is rejected before it can become an already-expired deadline
// handed to http.Server.
func TestFinalizeConfig_ServerTimeouts(t *testing.T) {
	t.Run("defaults are valid", func(t *testing.T) {
		dir := t.TempDir()
		if err := finalizeConfig(DefaultConfig(dir), dir); err != nil {
			t.Fatalf("finalizeConfig on defaults: %v", err)
		}
	})

	t.Run("negative read_timeout rejected", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir())
		cfg.Srv.ReadTimeout = -1
		err := finalizeConfig(cfg, t.TempDir())
		if err == nil {
			t.Fatal("a negative read_timeout must fail")
		}
		if !strings.Contains(err.Error(), "server.read_timeout") {
			t.Errorf("error %q does not name the offending key", err)
		}
	})
}

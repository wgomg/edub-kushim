package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReload_Success(t *testing.T) {
	configDir := t.TempDir()
	writeMinimalConfig(t, configDir)

	cfg := DefaultConfig(configDir)
	cfg.Consumer.Polling.Interval = 99 // will be overwritten

	ok, err := Reload(configDir, cfg)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !ok {
		t.Fatal("Reload returned false, want true")
	}
	if cfg.App.ConfigDir != configDir {
		t.Errorf("ConfigDir = %q, want %q", cfg.App.ConfigDir, configDir)
	}
}

func TestReload_MissingFile(t *testing.T) {
	configDir := t.TempDir()

	cfg := DefaultConfig(configDir)
	origInterval := cfg.Consumer.Polling.Interval

	ok, err := Reload(configDir, cfg)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if ok {
		t.Fatal("Reload returned true for missing file, want false")
	}
	if cfg.Consumer.Polling.Interval != origInterval {
		t.Errorf("cfg modified despite missing file: Interval = %d, want %d",
			cfg.Consumer.Polling.Interval, origInterval)
	}
}

func TestReload_InvalidYAML(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("{{{{bad"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig(configDir)
	ok, err := Reload(configDir, cfg)
	if ok {
		t.Fatal("Reload returned true for invalid YAML, want false")
	}
	if err == nil {
		t.Fatal("Reload returned nil error for invalid YAML")
	}
}

func TestReload_MissingOCRLanguages(t *testing.T) {
	configDir := t.TempDir()
	yaml := `consumer:
  ocr:
    languages: []
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig(configDir)
	cfg.Consumer.OCR.Languages = []string{"eng"} // set so we can verify it gets cleared

	ok, err := Reload(configDir, cfg)
	if ok {
		t.Fatal("Reload returned true for missing OCR languages, want false")
	}
	if err == nil {
		t.Fatal("Reload returned nil error for missing OCR languages")
	}
}

func TestReload_AppliesChanges(t *testing.T) {
	configDir := t.TempDir()
	writeMinimalConfig(t, configDir)

	cfg := DefaultConfig(configDir)
	if cfg.Consumer.Polling.Interval != 5 {
		t.Fatalf("default interval = %d, want 5", cfg.Consumer.Polling.Interval)
	}

	// Write config with a different polling interval
	yaml := `consumer:
  ocr:
    languages:
      - eng
  polling:
    interval: 15
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	ok, err := Reload(configDir, cfg)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !ok {
		t.Fatal("Reload returned false")
	}
	if cfg.Consumer.Polling.Interval != 15 {
		t.Errorf("Interval = %d, want 15", cfg.Consumer.Polling.Interval)
	}
}

func TestReload_ResetsRemovedKeys(t *testing.T) {
	configDir := t.TempDir()
	writeMinimalConfig(t, configDir)

	cfg := DefaultConfig(configDir)

	// First reload with a custom interval
	yaml := `consumer:
  ocr:
    languages:
      - eng
  polling:
    interval: 20
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	ok, err := Reload(configDir, cfg)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !ok || cfg.Consumer.Polling.Interval != 20 {
		t.Fatalf("first reload: ok=%v interval=%d", ok, cfg.Consumer.Polling.Interval)
	}

	// Second reload without polling section — should revert to default
	yaml2 := `consumer:
  ocr:
    languages:
      - eng
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml2), 0644); err != nil {
		t.Fatal(err)
	}
	ok, err = Reload(configDir, cfg)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !ok {
		t.Fatal("second reload returned false")
	}
	if cfg.Consumer.Polling.Interval != 5 {
		t.Errorf("Interval after removal = %d, want 5 (default)", cfg.Consumer.Polling.Interval)
	}
}

// writeMinimalConfig writes a valid config.yaml with the required OCR languages.
func writeMinimalConfig(t *testing.T, configDir string) {
	t.Helper()
	yaml := `consumer:
  ocr:
    languages:
      - eng
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
}

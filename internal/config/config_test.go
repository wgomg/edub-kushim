package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("consumer:\n  ocr:\n    languages: [eng]\n"), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.App.Env != "development" {
		t.Errorf("App.Env = %q, want %q", cfg.App.Env, "development")
	}
	if cfg.Srv.Port != 3000 {
		t.Errorf("Srv.Port = %d, want 3000", cfg.Srv.Port)
	}
	if cfg.Db.Type != "sqlite" {
		t.Errorf("Db.Type = %q, want sqlite", cfg.Db.Type)
	}
	if cfg.Db.Name != "edub.db" {
		t.Errorf("Db.Name = %q, want edub.db", cfg.Db.Name)
	}
	if cfg.Storage.ConsumptionDir != filepath.Join(dir, "inbox") {
		t.Errorf("Storage.ConsumptionDir = %q, want %q", cfg.Storage.ConsumptionDir, filepath.Join(dir, "inbox"))
	}
	if cfg.Storage.StorageDir != filepath.Join(dir, "storage") {
		t.Errorf("Storage.StorageDir = %q, want %q", cfg.Storage.StorageDir, filepath.Join(dir, "storage"))
	}
	if len(cfg.Consumer.SupportedFiles) != 1 || cfg.Consumer.SupportedFiles[0] != ".pdf" {
		t.Errorf("Consumer.SupportedFiles = %v", cfg.Consumer.SupportedFiles)
	}
	if cfg.Consumer.TextExtractor.Engine != "mupdf" {
		t.Errorf("Consumer.TextExtractor.Engine = %q, want mupdf", cfg.Consumer.TextExtractor.Engine)
	}
	if cfg.Consumer.Workers != 1 {
		t.Errorf("Consumer.Workers = %d, want 1", cfg.Consumer.Workers)
	}
}

func TestLoad_MissingOCRLanguages(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("{}\n"), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for missing ocr.languages")
	}
}

func TestLoad_ConfigFileNotFound(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("consumer:\n  ocr:\n    languages: [eng]\n"), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoad_OverridesFromFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
app:
  environment: production
  log_level: debug
server:
  port: 8080
consumer:
  ocr:
    languages: [eng, spa]
  workers: 4
`), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.App.Env != "production" {
		t.Errorf("App.Env = %q, want production", cfg.App.Env)
	}
	if cfg.App.LogLevel != "debug" {
		t.Errorf("App.LogLevel = %q, want debug", cfg.App.LogLevel)
	}
	if cfg.Srv.Port != 8080 {
		t.Errorf("Srv.Port = %d, want 8080", cfg.Srv.Port)
	}
	if len(cfg.Consumer.OCR.Languages) != 2 {
		t.Fatalf("OCR.Languages len = %d, want 2", len(cfg.Consumer.OCR.Languages))
	}
	if cfg.Consumer.OCR.Languages[0] != "eng" || cfg.Consumer.OCR.Languages[1] != "spa" {
		t.Errorf("OCR.Languages = %v", cfg.Consumer.OCR.Languages)
	}
	if cfg.Consumer.Workers != 4 {
		t.Errorf("Workers = %d, want 4", cfg.Consumer.Workers)
	}
}

func TestLoad_TildeExpansion(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("consumer:\n  ocr:\n    languages: [eng]\n    data_dir: ~/custom/tessdata\n"), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	home, _ := os.UserHomeDir()
	want := home + "/custom/tessdata"
	if cfg.Consumer.OCR.DataDir != want {
		t.Errorf("OCR.DataDir = %q, want %q", cfg.Consumer.OCR.DataDir, want)
	}
}

func TestLoad_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("consumer:\n  ocr:\n    languages: [eng]\n"), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := os.Stat(cfg.Storage.ConsumptionDir); os.IsNotExist(err) {
		t.Errorf("consumption dir not created: %s", cfg.Storage.ConsumptionDir)
	}
	if _, err := os.Stat(cfg.Storage.StorageDir); os.IsNotExist(err) {
		t.Errorf("storage dir not created: %s", cfg.Storage.StorageDir)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("{{{invalid\n"), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

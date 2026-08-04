package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestSaveMap_MergesExistingConfig(t *testing.T) {
	configDir := t.TempDir()

	initialYAML := `storage:
  consumption_dir: /custom/inbox
  storage_dir: /custom/storage
database:
  host: localhost
  port: 5433
consumer:
  polling:
    enabled: false
    interval: 10
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(initialYAML), 0644); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"consumer.polling.enabled": true,
	}
	if err := SaveMap(configDir, body); err != nil {
		t.Fatalf("SaveMap: %v", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(filepath.Join(configDir, "config.yaml"))
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig: %v", err)
	}

	if v.GetString("storage.consumption_dir") != "/custom/inbox" {
		t.Errorf("storage.consumption_dir = %q, want /custom/inbox", v.GetString("storage.consumption_dir"))
	}
	if v.GetString("storage.storage_dir") != "/custom/storage" {
		t.Errorf("storage.storage_dir = %q, want /custom/storage", v.GetString("storage.storage_dir"))
	}
	if v.GetString("database.host") != "localhost" {
		t.Errorf("database.host = %q, want localhost", v.GetString("database.host"))
	}
	if v.GetInt("database.port") != 5433 {
		t.Errorf("database.port = %d, want 5433", v.GetInt("database.port"))
	}
	if !v.GetBool("consumer.polling.enabled") {
		t.Error("consumer.polling.enabled should be true after merge")
	}
	if v.GetInt("consumer.polling.interval") != 10 {
		t.Errorf("consumer.polling.interval = %d, want 10", v.GetInt("consumer.polling.interval"))
	}
}

func TestSaveMap_NoExistingFile(t *testing.T) {
	configDir := t.TempDir()

	body := map[string]any{
		"consumer.polling.enabled": true,
	}
	if err := SaveMap(configDir, body); err != nil {
		t.Fatalf("SaveMap: %v", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(filepath.Join(configDir, "config.yaml"))
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig: %v", err)
	}
	if !v.GetBool("consumer.polling.enabled") {
		t.Error("consumer.polling.enabled should be true")
	}
}

func TestSaveMap_NewFileIsNotWorldReadable(t *testing.T) {
	configDir := t.TempDir()

	if err := SaveMap(configDir, map[string]any{"server.auth_enabled": true}); err != nil {
		t.Fatalf("SaveMap: %v", err)
	}

	info, err := os.Stat(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config.yaml mode = %o, want 0600", perm)
	}
}

func TestSaveMap_ExistingWorldReadableFileSelfHeals(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.yaml")

	if err := os.WriteFile(configPath, []byte("server:\n  auth_enabled: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := SaveMap(configDir, map[string]any{"server.auth_enabled": true}); err != nil {
		t.Fatalf("SaveMap: %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config.yaml mode after save = %o, want 0600 (should self-heal from 0644)", perm)
	}
}

func TestSaveMap_InvalidExistingFile(t *testing.T) {
	configDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("{{{{invalid"), 0644); err != nil {
		t.Fatal(err)
	}

	err := SaveMap(configDir, map[string]any{"key": "value"})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func seededConfigBody(configDir string) map[string]any {
	return map[string]any{
		"consumer.ocr.languages":                     []string{"eng"},
		"storage.consumption_dir":                    filepath.Join(configDir, "inbox"),
		"storage.storage_dir":                        filepath.Join(configDir, "storage"),
		"consumer.ocr.data_dir":                      filepath.Join(configDir, "tessdata"),
		"enricher.contentanalyzer.llm.request_delay": float64(2),
		"server.max_batch_delete":                    5,
	}
}

func TestValidateSave_AcceptsValidBody(t *testing.T) {
	configDir := t.TempDir()
	if err := SaveMap(configDir, seededConfigBody(configDir)); err != nil {
		t.Fatalf("seed SaveMap: %v", err)
	}

	body := map[string]any{"enricher.contentanalyzer.llm.request_delay": float64(2.5)}
	if err := ValidateSave(configDir, body); err != nil {
		t.Fatalf("ValidateSave: %v", err)
	}
}

func TestValidateSave_RejectsInvalidValueWithoutPersisting(t *testing.T) {
	configDir := t.TempDir()
	if err := SaveMap(configDir, seededConfigBody(configDir)); err != nil {
		t.Fatalf("seed SaveMap: %v", err)
	}

	err := ValidateSave(configDir, map[string]any{"enricher.contentanalyzer.llm.request_delay": float64(-1)})
	if err == nil {
		t.Fatal("expected error for negative request_delay")
	}
	if !strings.Contains(err.Error(), "request_delay") {
		t.Errorf("error %q does not mention request_delay", err)
	}

	cfg, err := Load(configDir)
	if err != nil {
		t.Fatalf("Load after rejected save: %v", err)
	}
	if got := cfg.Enricher.ContentAnalyzer.Llm.RequestDelay; got != 2 {
		t.Errorf("on-disk request_delay = %v, want 2 (rejected save must not persist)", got)
	}
}

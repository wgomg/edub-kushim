package config

import (
	"os"
	"path/filepath"
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

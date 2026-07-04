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

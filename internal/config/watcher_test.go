package config

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

const testWatcherInterval = 50 * time.Millisecond

func writeTestConfig(t *testing.T, configDir string) {
	t.Helper()
	content := []byte(`consumer:
  ocr:
    languages:
      - eng
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestWatcher_FiresOnChange(t *testing.T) {
	configDir := t.TempDir()
	writeTestConfig(t, configDir)

	var count atomic.Int32
	var lastCfg atomic.Pointer[Config]
	w := NewWatcher(configDir, testWatcherInterval, func(cfg *Config) {
		count.Add(1)
		lastCfg.Store(cfg)
	}, utils.NewDiscardLogger())
	w.Start()
	defer w.Stop()

	time.Sleep(testWatcherInterval * 3)
	startCount := count.Load()

	path := filepath.Join(configDir, "config.yaml")
	modified := []byte(`consumer:
  ocr:
    languages:
      - eng
  polling:
    interval: 10
`)
	if err := os.WriteFile(path, modified, 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(testWatcherInterval * 3)

	n := count.Load()
	if n != startCount+1 {
		t.Errorf("callback invoked %d times total after change, want %d", n, startCount+1)
	}
	cfg := lastCfg.Load()
	if cfg != nil && cfg.Consumer.Polling.Interval != 10 {
		t.Errorf("reloaded polling interval = %d, want 10", cfg.Consumer.Polling.Interval)
	}
}

func TestWatcher_NoFireOnMissingFile(t *testing.T) {
	configDir := t.TempDir()

	var count atomic.Int32
	w := NewWatcher(configDir, testWatcherInterval, func(cfg *Config) {
		count.Add(1)
	}, utils.NewDiscardLogger())
	w.Start()
	defer w.Stop()

	time.Sleep(testWatcherInterval * 3)

	if n := count.Load(); n != 0 {
		t.Errorf("callback invoked %d times on missing file, want 0", n)
	}
}

func TestWatcher_NoFireOnInvalidYAML(t *testing.T) {
	configDir := t.TempDir()
	writeTestConfig(t, configDir)

	var count atomic.Int32
	w := NewWatcher(configDir, testWatcherInterval, func(cfg *Config) {
		count.Add(1)
	}, utils.NewDiscardLogger())
	w.Start()
	defer w.Stop()

	time.Sleep(testWatcherInterval * 3)
	startCount := count.Load()

	path := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(path, []byte("{{{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(testWatcherInterval * 3)

	if n := count.Load(); n != startCount {
		t.Errorf("callback invoked %d times after invalid write, want %d (no change)", n, startCount)
	}
}

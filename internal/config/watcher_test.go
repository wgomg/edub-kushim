package config

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
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
	}, log.NewDiscardLogger())
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
	}, log.NewDiscardLogger())
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
	}, log.NewDiscardLogger())
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

// TestWatcher_FutureMtimeDoesNotFreeze pins the content-based change signal against its old failure
// mode: a file stamped with a future modification time — restored with preserved timestamps, or
// written under clock skew — must not suppress the reload of a later, genuinely newer configuration.
func TestWatcher_FutureMtimeDoesNotFreeze(t *testing.T) {
	configDir := t.TempDir()
	writeTestConfig(t, configDir)
	path := filepath.Join(configDir, "config.yaml")

	var count atomic.Int32
	var lastCfg atomic.Pointer[Config]
	w := NewWatcher(configDir, testWatcherInterval, func(cfg *Config) {
		count.Add(1)
		lastCfg.Store(cfg)
	}, log.NewDiscardLogger())
	w.Start()
	defer w.Stop()

	time.Sleep(testWatcherInterval * 3)
	if n := count.Load(); n != 1 {
		t.Fatalf("callback invoked %d times on the initial config, want 1", n)
	}

	future := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

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

	if n := count.Load(); n != 2 {
		t.Fatalf("callback invoked %d times after the change, want 2 (a future mtime must not freeze the loop)", n)
	}
	if cfg := lastCfg.Load(); cfg != nil && cfg.Consumer.Polling.Interval != 10 {
		t.Errorf("reloaded polling interval = %d, want 10", cfg.Consumer.Polling.Interval)
	}
}

// TestWatcher_IdenticalRewriteDoesNotFire pins the other half of the content-based signal: a rewrite
// that leaves the bytes untouched only bumps the modification time, which is not a configuration
// change and must not reload anything.
func TestWatcher_IdenticalRewriteDoesNotFire(t *testing.T) {
	configDir := t.TempDir()
	writeTestConfig(t, configDir)
	path := filepath.Join(configDir, "config.yaml")

	var count atomic.Int32
	w := NewWatcher(configDir, testWatcherInterval, func(cfg *Config) {
		count.Add(1)
	}, log.NewDiscardLogger())
	w.Start()
	defer w.Stop()

	time.Sleep(testWatcherInterval * 3)
	if n := count.Load(); n != 1 {
		t.Fatalf("callback invoked %d times on the initial config, want 1", n)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(testWatcherInterval * 3)

	if n := count.Load(); n != 1 {
		t.Errorf("callback invoked %d times after a rewrite with identical bytes, want 1", n)
	}
}

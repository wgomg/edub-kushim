package config

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
)

// Watcher polls the config file's modification time and reloads the
// configuration through it, handing the fresh tree to the callback when it
// actually changed. Invalid YAML logs an error and keeps the previous
// tree.
type Watcher struct {
	configDir string
	interval  time.Duration
	onReload  func(*Config)
	logger    *log.Logger

	mu      sync.Mutex
	lastMod time.Time
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewWatcher prepares the reload loop; Start runs it, Stop ends it.
func NewWatcher(configDir string, interval time.Duration, onReload func(*Config), logger *log.Logger) *Watcher {
	return &Watcher{
		configDir: configDir,
		interval:  interval,
		onReload:  onReload,
		logger:    logger,
	}
}

// Start begins the reload loop; a second call does nothing.
func (w *Watcher) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.ctx != nil {
		return
	}

	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.wg.Add(1)
	go w.run()
}

// Stop ends the loop and waits for it. Safe before Start.
func (w *Watcher) Stop() {
	w.mu.Lock()
	if w.cancel == nil {
		w.mu.Unlock()
		return
	}
	cancel := w.cancel
	w.mu.Unlock()

	cancel()
	w.wg.Wait()
}

func (w *Watcher) run() {
	defer w.wg.Done()

	w.check()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.check()
		}
	}
}

func (w *Watcher) check() {
	configPath := filepath.Join(w.configDir, "config.yaml")

	info, err := os.Stat(configPath)
	if err != nil {
		return
	}

	w.mu.Lock()
	lastMod := w.lastMod
	w.mu.Unlock()

	if !info.ModTime().After(lastMod) {
		return
	}

	cfg, err := Load(w.configDir)
	if err != nil {
		w.logger.Error(nil, "config file changed, but reload failed: %v", err)
		return
	}

	w.mu.Lock()
	w.lastMod = info.ModTime()
	w.mu.Unlock()

	w.logger.Info(nil, "config file changed, reloading...")
	w.onReload(cfg)
}

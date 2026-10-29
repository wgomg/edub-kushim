package config

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
)

// Watcher polls the config file's content and reloads the configuration through it, handing the fresh
// tree to the callback when the content actually changed. The content itself is the change signal, so a
// restored or clock-skewed modification time cannot freeze the loop, and a rewrite with identical bytes
// does not fire. A change is applied only once two consecutive polls observe the same bytes, so the
// truncated or partially written file that a non-atomic save briefly leaves on disk is never taken for
// a configuration. Invalid YAML logs an error and keeps the previous tree, retrying for as long as it
// stays invalid. The callback receives an applied change once; a callback that can fail must arrange its
// own redelivery.
type Watcher struct {
	configDir string
	interval  time.Duration
	onReload  func(*Config)
	logger    *log.Logger

	mu          sync.Mutex
	seenSum     [sha256.Size]byte
	haveSeen    bool
	appliedSum  [sha256.Size]byte
	haveApplied bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
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
	data, err := os.ReadFile(filepath.Join(w.configDir, "config.yaml"))
	if err != nil {
		return
	}

	sum := sha256.Sum256(data)

	w.mu.Lock()
	stable := w.haveSeen && w.seenSum == sum
	fresh := !w.haveApplied || w.appliedSum != sum
	w.seenSum = sum
	w.haveSeen = true
	w.mu.Unlock()

	if !stable || !fresh {
		return
	}

	cfg, err := parseConfig(w.configDir, data)
	if err != nil {
		w.logger.Error("", "config file changed, but reload failed: %v", err)
		return
	}

	w.mu.Lock()
	w.appliedSum = sum
	w.haveApplied = true
	w.mu.Unlock()

	w.logger.Info("", "config file changed, reloading...")
	w.onReload(cfg)
}

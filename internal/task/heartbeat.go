package task

import (
	"context"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
)

// Heartbeat keeps a batch owner alive on a ticker until Stop or context
// cancellation. A failed or zero-row heartbeat is logged, never fatal.
type Heartbeat struct {
	owner    *Owner
	interval time.Duration
	done     chan struct{}
	once     sync.Once
	logger   *log.Logger
}

// NewHeartbeat prepares the ticker for one batch owner. logger must be
// non-nil: the loop logs without a guard.
func NewHeartbeat(owner *Owner, interval time.Duration, logger *log.Logger) *Heartbeat {
	return &Heartbeat{
		owner:    owner,
		interval: interval,
		done:     make(chan struct{}),
		logger:   logger,
	}
}

// Start runs the heartbeat loop until Stop or ctx cancellation.
func (h *Heartbeat) Start(ctx context.Context) {
	go func() {
		h.logger.Info("", "heartbeat: started (ownerID=%s)", h.owner.ownerID)
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rows, err := h.owner.Heartbeat(ctx)
				if err != nil {
					h.logger.Error("", "heartbeat: %v", err)
				} else if rows == 0 {
					h.logger.Error("", "heartbeat: updated 0 rows (ownerID=%s — row missing or owner_id mismatch)", h.owner.ownerID)
				} else {
					h.logger.Debug("", "heartbeat: tick ok (ownerID=%s)", h.owner.ownerID)
				}
			case <-h.done:
				h.logger.Debug("", "heartbeat: stopped via done channel (ownerID=%s)", h.owner.ownerID)
				return
			case <-ctx.Done():
				h.logger.Debug("", "heartbeat: stopped via ctx.Done (ownerID=%s)", h.owner.ownerID)
				return
			}
		}
	}()
}

// Stop ends the heartbeat loop exactly once.
func (h *Heartbeat) Stop() {
	h.once.Do(func() {
		close(h.done)
	})
}

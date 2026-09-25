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

// NewHeartbeat prepares the ticker for one batch owner.
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
		if h.logger != nil {
			h.logger.Info(nil, "heartbeat: started (ownerID=%s)", h.owner.OwnerID)
		}
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rows, err := h.owner.Heartbeat(ctx)
				if err != nil {
					if h.logger != nil {
						h.logger.Error(nil, "heartbeat: %v", err)
					}
				} else if rows == 0 {
					if h.logger != nil {
						h.logger.Error(nil, "heartbeat: updated 0 rows (ownerID=%s — row missing or owner_id mismatch)", h.owner.OwnerID)
					}
				} else if h.logger != nil {
					h.logger.Debug(nil, "heartbeat: tick ok (ownerID=%s)", h.owner.OwnerID)
				}
			case <-h.done:
				if h.logger != nil {
					h.logger.Debug(nil, "heartbeat: stopped via done channel (ownerID=%s)", h.owner.OwnerID)
				}
				return
			case <-ctx.Done():
				if h.logger != nil {
					h.logger.Debug(nil, "heartbeat: stopped via ctx.Done (ownerID=%s)", h.owner.OwnerID)
				}
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

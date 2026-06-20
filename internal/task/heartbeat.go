package task

import (
	"context"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

type Heartbeat struct {
	owner    *Owner
	interval time.Duration
	done     chan struct{}
	once     sync.Once
	logger   *utils.Logger
}

func NewHeartbeat(owner *Owner, interval time.Duration, logger *utils.Logger) *Heartbeat {
	return &Heartbeat{
		owner:    owner,
		interval: interval,
		done:     make(chan struct{}),
		logger:   logger,
	}
}

func (h *Heartbeat) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := h.owner.Heartbeat(ctx); err != nil {
					if h.logger != nil {
						h.logger.Error(nil, "heartbeat: %v", err)
					}
				}
			case <-h.done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (h *Heartbeat) Stop() {
	h.once.Do(func() {
		close(h.done)
	})
}

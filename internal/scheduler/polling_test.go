package scheduler

import (
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestPollingScheduler_Lifecycle(t *testing.T) {
	s := NewPollingScheduler(
		func() config.PollingConfig {
			return config.PollingConfig{Enabled: false, Interval: 5}
		},
		testutil.NewTestLogger(),
		"",
		pool.NewSemaphore(2),
	)

	s.Start()
	// Idempotent start must not panic.
	s.Start()

	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not complete within 3 seconds")
	}

	// Idempotent stop must not panic.
	s.Stop()
}

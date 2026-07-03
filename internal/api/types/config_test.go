package types

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
)

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

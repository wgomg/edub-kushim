package wizard

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
)

// Start must assemble its routes without a database client: the wizard runs
// before bootstrap exists, so a nil client has to answer requests, not panic.
func TestStart_NilClientServesRoutes(t *testing.T) {
	// an empty HOME has no config.yaml, so Start skips the DB auto-resume
	t.Setenv("HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	s := NewServer(addr, log.NewDiscardLogger())
	startErr := make(chan error, 1)
	go func() { startErr <- s.Start() }()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	client := &http.Client{Timeout: time.Second}
	base := "http://" + addr

	var lastErr error
	served := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-startErr:
			t.Fatalf("Start returned before serving: %v", err)
		default:
		}

		resp, err := client.Get(base + "/wizard/config/status")
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		var status map[string]any
		err = json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decode status response: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status route returned %d", resp.StatusCode)
		}
		if _, ok := status["configured"]; !ok {
			t.Fatalf("status payload missing configured: %v", status)
		}
		served = true
		break
	}
	if !served {
		t.Fatalf("wizard status route never became ready: %v", lastErr)
	}

	resp, err := client.Get(base + "/")
	if err != nil {
		t.Fatalf("get wizard root: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("static route returned %d, want 200", resp.StatusCode)
	}
}

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/container"
)

// TestTagCommandsDispatchHelp pins that both tag verbs are registered in the cli
// table and answer --help through the dispatcher, the path cmd/kushim takes.
func TestTagCommandsDispatchHelp(t *testing.T) {
	c, _ := newTestContainer(t)
	runner := NewCommandRunner(c, "cli")

	for _, name := range []string{"tagidentity", "tagreconcile"} {
		if err := runner.ExecuteCommand(name, []string{"--help"}); err != nil {
			t.Errorf("%s --help: %v", name, err)
		}
	}
}

// TestTagReconcileValidatesRunID pins the fork mode's contract: --run takes a batch
// id and anything else is rejected before a pass is launched.
func TestTagReconcileValidatesRunID(t *testing.T) {
	c, _ := newTestContainer(t)
	runner := NewCommandRunner(c, "cli")

	err := runner.ExecuteCommand("tagreconcile", []string{"--run", "not-a-uuid"})
	if err == nil || !strings.Contains(err.Error(), "invalid batch id") {
		t.Errorf("--run with a non-uuid: got %v, want an invalid-batch-id error", err)
	}

	err = runner.ExecuteCommand("tagreconcile", []string{"--bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("unknown flag: got %v, want an unknown-flag error", err)
	}
}

// TestTagCommandsWithoutFlagsEnqueue pins the two-mode contract: with no flags both
// verbs reach the enqueue path instead of demanding --run, and only report the
// database here — the container points at a dead port, so the enqueue itself is
// exercised down to the connection.
func TestTagCommandsWithoutFlagsEnqueue(t *testing.T) {
	configDir := t.TempDir()
	yaml := `consumer:
  ocr:
    languages:
      - eng
database:
  host: 127.0.0.1
  port: 1
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	c := container.NewContainer(cfg, log.NewLogger("error"))
	t.Cleanup(func() { c.Close() })

	runner := NewCommandRunner(c, "cli")
	for _, name := range []string{"tagidentity", "tagreconcile"} {
		err := runner.ExecuteCommand(name, nil)
		if err == nil {
			t.Errorf("%s without flags: want an unreachable-database error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), "database") {
			t.Errorf("%s without flags must reach the enqueue path, got %v", name, err)
		}
		if strings.Contains(err.Error(), "--run") {
			t.Errorf("%s without flags must not demand --run, got %v", name, err)
		}
	}
}

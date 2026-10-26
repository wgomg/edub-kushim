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

// TestTagCommandsDispatchHelp pins that the tag verbs are registered in the cli
// table and answer --help through the dispatcher, the path cmd/kushim takes.
func TestTagCommandsDispatchHelp(t *testing.T) {
	c, _ := newTestContainer(t)
	runner := NewCommandRunner(c, "cli")

	for _, name := range []string{"tagidentity", "tagreconcile", "tagmeasure", "tagaudit"} {
		if err := runner.ExecuteCommand(name, []string{"--help"}); err != nil {
			t.Errorf("%s --help: %v", name, err)
		}
	}
}

// TestTagCommandsValidateRunID pins the batch-mode contract across the tag verbs: --run is the deprecated
// alias of --from-batch and requires the execution token, and a malformed token is rejected before a pass
// is launched.
func TestTagCommandsValidateRunID(t *testing.T) {
	c, _ := newTestContainer(t)
	runner := NewCommandRunner(c, "cli")

	for _, name := range []string{"tagidentity", "tagreconcile", "tagmeasure", "tagaudit"} {
		err := runner.ExecuteCommand(name, []string{"--run", "not-a-uuid"})
		if err == nil || !strings.Contains(err.Error(), "--exec-token") {
			t.Errorf("%s --run without a token: got %v, want the deprecation refusal pointing at --exec-token", name, err)
		}

		err = runner.ExecuteCommand(name, []string{"--from-batch", "batch-1", "--exec-token", "not-a-uuid"})
		if err == nil || !strings.Contains(err.Error(), "invalid exec token") {
			t.Errorf("%s --exec-token with a non-uuid: got %v, want an invalid-token error", name, err)
		}

		err = runner.ExecuteCommand(name, []string{"--bogus"})
		if err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s unknown flag: got %v, want an unknown-flag error", name, err)
		}
	}
}

// TestTagCommandsWithoutFlagsRunDirect pins the two-mode contract: with no flags the tag verbs run the pass
// directly instead of demanding --run or enqueueing, and only report the database here — the container
// points at a dead port, so the direct run is exercised down to the connection.
func TestTagCommandsWithoutFlagsRunDirect(t *testing.T) {
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
	for _, name := range []string{"tagidentity", "tagreconcile", "tagmeasure", "tagaudit"} {
		err := runner.ExecuteCommand(name, nil)
		if err == nil {
			t.Errorf("%s without flags: want an unreachable-database error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), "database") {
			t.Errorf("%s without flags must reach the direct-run path, got %v", name, err)
		}
		if strings.Contains(err.Error(), "--run") {
			t.Errorf("%s without flags must not demand --run, got %v", name, err)
		}
	}
}

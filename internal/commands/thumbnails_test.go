package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestThumbnailsHandler_Help(t *testing.T) {
	c, _ := newTestContainer(t)
	err := backfillThumbnailsHandler(c, []string{"--help"})
	if err != nil {
		t.Errorf("backfillThumbnailsHandler --help returned error: %v", err)
	}
}

func TestThumbnailsHandler_RequiresExactlyOneMode(t *testing.T) {
	c, _ := newTestContainer(t)
	argsList := [][]string{
		{},
		{"--all", "--batch", "b1"},
		{"--all", "--document", "d1"},
		{"--batch", "b1", "--document", "d1"},
	}
	for _, args := range argsList {
		if err := backfillThumbnailsHandler(c, args); err == nil {
			t.Errorf("expected error for args %v", args)
		}
	}
}

func TestThumbnailsHandler_DisabledConfigGate(t *testing.T) {
	configDir := t.TempDir()
	yaml := `consumer:
  thumbnail:
    enabled: false
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
	c := NewContainer(cfg, utils.NewLogger("error"))
	t.Cleanup(func() { c.Close() })

	err = backfillThumbnailsHandler(c, []string{"--all"})
	if err == nil || !strings.Contains(err.Error(), "thumbnails are disabled") {
		t.Fatalf("expected disabled-thumbnails error, got %v", err)
	}

	err = backfillThumbnailsHandler(c, []string{"--all", "--force"})
	if err == nil {
		t.Fatal("expected error (database unreachable), got nil")
	}
	if strings.Contains(err.Error(), "thumbnails are disabled") {
		t.Fatalf("--force should bypass the disabled gate, got %v", err)
	}
}
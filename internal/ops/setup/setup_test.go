package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/log"
)

// RunSetup must refuse over any existing configuration before it parses
// flags or reaches a database: the refusal is the interlock that keeps
// --reset-database away from a live install.
func TestRunSetup_RefusesExistingConfiguration(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "wizard defaults", args: nil},
		{name: "cli", args: []string{"--cli"}},
		{name: "cli with reset", args: []string{"--cli", "--reset-database"}},
		{name: "reset without cli", args: []string{"--reset-database"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			configDir := filepath.Join(home, ".config", "edub-kushim")
			if err := os.MkdirAll(configDir, 0755); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(configDir, "config.yaml")
			if err := os.WriteFile(configPath, []byte("server:\n  auth_enabled: false\n"), 0600); err != nil {
				t.Fatal(err)
			}

			err := RunSetup(tc.args, log.NewDiscardLogger())
			if err == nil {
				t.Fatal("RunSetup must refuse over an existing configuration")
			}
			if !strings.Contains(err.Error(), configPath) {
				t.Errorf("error %q must name the existing config path %q", err, configPath)
			}
		})
	}
}

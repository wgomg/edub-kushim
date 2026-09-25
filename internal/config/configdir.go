package config

import (
	"os"
	"path/filepath"
)

// ConfigDir returns the application's default config directory under the
// user's home.
func ConfigDir() (*string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configDir := filepath.Join(home, ".config", "edub-kushim")

	return &configDir, nil
}

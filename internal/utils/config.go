package utils

import (
	"os"
	"path/filepath"
)

func ConfigDir() (*string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configDir := filepath.Join(home, ".config", "edub-kushim")

	return &configDir, nil
}

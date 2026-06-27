package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func KushimBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		kushimPath := filepath.Join(filepath.Dir(exe), "kushim")
		if _, err := os.Stat(kushimPath); err == nil {
			return kushimPath, nil
		}
	}
	kushimPath, err := exec.LookPath("kushim")
	if err != nil {
		return "", fmt.Errorf("kushim not found in PATH and not found as sibling of edub binary")
	}
	return kushimPath, nil
}

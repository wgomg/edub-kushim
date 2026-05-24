package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/utils"
)

func TestSetupHandler(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := setupHandler(c, []string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunSetup_MissingLangs(t *testing.T) {
	err := RunSetup([]string{}, utils.NewDiscardLogger())
	if err == nil {
		t.Fatal("expected error for missing --langs")
	}
}

func TestRunSetup_HelpShowsUsage(t *testing.T) {
	err := RunSetup([]string{"--help"}, utils.NewDiscardLogger())
	if err == nil {
		t.Fatal("expected error for missing --langs (--help is not special-cased)")
	}
}

func TestRunSetup_CreatesConfig(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	inboxDir := filepath.Join(dir, "inbox")
	storageDir := filepath.Join(dir, "storage")
	dbPath := filepath.Join(dir, "data")

	err := RunSetup([]string{
		"--langs", "eng",
		"--config-dir", configDir,
		"--inbox-dir", inboxDir,
		"--storage-dir", storageDir,
		"--db-path", dbPath,
	}, utils.NewDiscardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config.yaml was not created")
	}

	tessdataDir := filepath.Join(configDir, "ocr", "tessdata")
	if _, err := os.Stat(tessdataDir); os.IsNotExist(err) {
		t.Fatal("tessdata dir was not created")
	}
}

func TestRunSetup_DefaultsHomeDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	err := RunSetup([]string{"--langs", "eng"}, utils.NewDiscardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configPath := filepath.Join(dir, ".config", "kushim", "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config.yaml was not created in default location")
	}
}

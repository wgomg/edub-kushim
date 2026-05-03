package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wgomg/edub-kushim/internal/commands"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func main() {
	if len(os.Args) < 2 {
		commands.PrintUsage()
	}

	commandName := os.Args[1]
	args := os.Args[2:]

	if commandName == "setup" {
		startupLogger := utils.NewLogger("info")
		if err := commands.RunSetup(args, startupLogger); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	startupLogger := utils.NewLogger("error")

	home, err := os.UserHomeDir()
	if err != nil {
		startupLogger.Fatal("Cannot determine home directory:", err)
	}
	configDir := filepath.Join(home, ".config", "kushim")

	cfg, err := config.Load(configDir)
	if err != nil {
		if strings.Contains(err.Error(), "ocr_languages is required") {
			startupLogger.Fatal("Not initialized: run 'kushim setup --langs eng,spa,...' first")
		}
		startupLogger.Fatal("Failed to load configuration:", err)
	}

	logger := utils.NewLogger(cfg.App.LogLevel)
	container := commands.NewContainer(cfg, logger)
	defer container.Close()

	runner := commands.NewCommandRunner(container)

	if err := runner.ExecuteCommand(commandName, args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

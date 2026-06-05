package main

import (
	"fmt"
	"os"
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

	if commandName == "--help" || commandName == "-h" {
		commands.PrintUsage()
		return
	}

	startupLogger := utils.NewLogger("error")
	configDir, err := utils.ConfigDir()
	if err != nil {
		startupLogger.Fatal("Cannot determine home directory:", err)
	}

	cfg, err := config.Load(*configDir)
	if err != nil {
		if strings.Contains(err.Error(), "ocr.languages is required") {
			startupLogger.Fatal("Not initialized: run 'kushim setup --langs eng,spa,...' first")
		}
		startupLogger.Fatal("Failed to load configuration:", err)
	}

	logger := utils.NewLogger(cfg.App.LogLevel)
	if cfg.App.LogFile != "" {
		if err := logger.SetLogFile(cfg.App.LogFile); err != nil {
			logger.Error(nil, "failed to open log file: %v", err)
		}
	}
	container := commands.NewContainer(cfg, logger)
	defer container.Close()

	runner := commands.NewCommandRunner(container, "cli")

	if err := runner.ExecuteCommand(commandName, args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

package main

import (
	"fmt"
	"os"

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

	startupLogger := utils.NewLogger("error")

	cfg, err := config.Load("./config.yaml")
	if err != nil {
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

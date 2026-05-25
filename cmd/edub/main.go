package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/api"
	"github.com/wgomg/edub-kushim/internal/commands"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func main() {
	var configPath string
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
			break
		}
	}

	if len(os.Args) < 2 {
		startServer(configPath)
		return
	}

	cmd := os.Args[1]

	if cmd == "--help" || cmd == "-h" {
		commands.PrintServerUsage()
	}

	runner := commands.NewCommandRunner(nil, "server")
	if err := runner.ExecuteCommand(cmd, os.Args[2:]); err != nil {
		startServer(configPath)
	}
}

func startServer(configPath string) {
	startupLogger := utils.NewLogger("error")

	var configDir string
	if configPath != "" {
		configDir = configPath
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			startupLogger.Fatal("Cannot determine home directory:", err)
		}
		configDir = filepath.Join(home, ".config", "kushim")
	}

	cfg, err := config.Load(configDir)
	if err != nil {
		startupLogger.Fatal("Failed to load configuration:", err)
	}

	logger := utils.NewLogger(cfg.App.LogLevel)
	logger.Info(nil, "Starting App...")
	logger.Info(nil, "Environment: %s", cfg.App.Env)
	logger.Info(nil, "Log level: %s", cfg.App.LogLevel)

	db, err := database.NewSQLiteDB(cfg.Db)
	if err != nil {
		log := utils.NewLogger("error")
		log.Fatal("Unable to establish database connection:", err)
	}

	srv := api.NewServer(*cfg, logger, db)

	// Graceful shutdown on SIGINT/SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info(nil, "Server starting on %s", srv.Addr())
		if err := srv.Start(); err != nil {
			logger.Fatal("Server failed to start:", err)
		}
	}()

	sig := <-quit
	logger.Info(nil, "Received signal %v, shutting down...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Server forced to shutdown:", err)
	}

	logger.Info(nil, "Server stopped")
}

package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/api"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func main() {
	if len(os.Args) < 2 {
		startServer()
		return
	}

	cmd := os.Args[1]

	if cmd == "--help" || cmd == "-h" {
		printUsage()
	}

	if err := Execute(cmd, os.Args[2:]); err != nil {
		startServer()
	}
}

func startServer() {
	startupLogger := utils.NewLogger("error")

	configDir, err := utils.ConfigDir()
	if err != nil {
		startupLogger.Fatal("Cannot determine home directory:", err)
	}

	cfg, err := config.Load(*configDir)
	if err != nil {
		if strings.Contains(err.Error(), "ocr.languages is required") {
			startupLogger.Fatal("Not initialized: run 'kushim setup --languages eng,spa,...' first")
		}
		startupLogger.Fatal("Failed to load configuration:", err)
	}

	logger := utils.NewLogger(cfg.App.LogLevel)
	logFile := filepath.Join(*configDir, "logs", "edub.log")
	os.MkdirAll(filepath.Dir(logFile), 0755)
	if err := logger.SetLogFile(utils.LogFileConfig{
		Path:       logFile,
		MaxSize:    cfg.App.Logging.MaxSize,
		MaxBackups: cfg.App.Logging.MaxBackups,
		MaxAge:     cfg.App.Logging.MaxAge,
		Compress:   cfg.App.Logging.Compress,
	}); err != nil {
		logger.Error(nil, "failed to open log file: %v", err)
	}
	logger.Info(nil, "Starting App...")
	logger.Info(nil, "Environment: %s", cfg.App.Env)
	logger.Info(nil, "Log level: %s", cfg.App.LogLevel)

	db, err := database.NewSQLiteDB(cfg.Db.Path, cfg.Db.Name)
	if err != nil {
		log := utils.NewLogger("error")
		log.Fatal("Unable to establish database connection:", err)
	}

	if err := database.InitializeSchema(db); err != nil {
		logger.Fatal("Unable to initialize database schema:", err)
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

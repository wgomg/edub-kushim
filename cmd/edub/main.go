// The edub binary: the REST API server and the SPA, built without CGo.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/api"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
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
	startupLogger := log.NewLogger("error")

	configDir, err := config.ConfigDir()
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

	logger := log.NewLogger(cfg.App.LogLevel)
	slog.SetDefault(logger.SlogLogger())
	logFile := filepath.Join(*configDir, "logs", "edub.log")
	_ = os.MkdirAll(filepath.Dir(logFile), 0755)
	if err := logger.SetLogFile(log.LogFileConfig{
		Path:       logFile,
		MaxSize:    cfg.App.Logging.MaxSize,
		MaxBackups: cfg.App.Logging.MaxBackups,
		MaxAge:     cfg.App.Logging.MaxAge,
		Compress:   cfg.App.Logging.Compress,
	}); err != nil {
		logger.Error("", "failed to open log file: %v", err)
	}
	logger.Info("", "Starting App...")
	logger.Info("", "Environment: %s", cfg.App.Env)
	logger.Info("", "Log level: %s", cfg.App.LogLevel)

	db, err := database.NewPostgresDB(config.BuildPostgresDSN(cfg.Db))
	if err != nil {
		errLogger := log.NewLogger("error")
		errLogger.Fatal("Unable to establish database connection:", err)
	}

	if err := database.InitializeSchema(context.Background(), db); err != nil {
		logger.Fatal("Unable to initialize database schema:", err)
	}

	srv := api.NewServer(*cfg, logger, db)

	// Graceful shutdown on SIGINT/SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("", "Server starting on %s", srv.Addr())
		if err := srv.Start(); err != nil {
			logger.Fatal("Server failed to start:", err)
		}
	}()

	sig := <-quit
	logger.Info("", "Received signal %v, shutting down...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Server forced to shutdown:", err)
	}

	logger.Info("", "Server stopped")
}

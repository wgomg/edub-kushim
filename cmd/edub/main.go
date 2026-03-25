package main

import (
	"github.com/wgomg/edub-kushim/internal/api"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func main() {
	startupLogger := utils.NewLogger("error")

	cfg, err := config.Load("./config.yaml")
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

	srv := api.NewServer(cfg.Srv, logger, db)

	logger.Info(nil, "Server starting on %s:%d", cfg.Srv.Host, cfg.Srv.Port)

	if err := srv.Start(); err != nil {
		logger.Fatal("Server failed to start:", err)
	}
}

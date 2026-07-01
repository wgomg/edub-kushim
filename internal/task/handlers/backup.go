package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type BackupTaskHandler struct {
	config   *config.Config
	logger   *utils.Logger
	backupMu *sync.RWMutex
}

func NewBackupTaskHandler(cfg *config.Config, logger *utils.Logger, backupMu *sync.RWMutex) *BackupTaskHandler {
	return &BackupTaskHandler{
		config:   cfg,
		logger:   logger,
		backupMu: backupMu,
	}
}

func (h *BackupTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	if !h.backupMu.TryLock() {
		return nil, fmt.Errorf("backup already in progress")
	}
	defer h.backupMu.Unlock()

	h.logger.Info(nil, "starting scheduled backup")

	dbPath := filepath.Join(h.config.Db.Path, h.config.Db.Name)
	configPath := filepath.Join(h.config.App.ConfigDir, "config.yaml")

	result, err := backup.Create(ctx, dbPath, h.config.Backup.Path, configPath, h.config.Storage.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	if err := backup.ApplyRetention(h.config.Backup.Path, h.config.Backup.Keep); err != nil {
		h.logger.Error(nil, "retention cleanup: %v", err)
	}

	h.logger.Info(nil, "backup completed: %s (%d bytes)", result.Path, result.SizeBytes)

	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}

	return raw, nil
}

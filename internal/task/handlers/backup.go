package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

var _ task.Dedupable = (*BackupTaskHandler)(nil)

type BackupTaskHandler struct {
	db        *sql.DB
	queries   *database.Queries
	getConfig func() *config.Config
	logger    *utils.Logger
}

func NewBackupTaskHandler(db *sql.DB, queries *database.Queries, getConfig func() *config.Config, logger *utils.Logger) *BackupTaskHandler {
	return &BackupTaskHandler{
		db:        db,
		queries:   queries,
		getConfig: getConfig,
		logger:    logger,
	}
}

func (h *BackupTaskHandler) DedupKey(payload json.RawMessage) string {
	return fmt.Sprintf("backup:%s", time.Now().UTC().Format("2006-01-02"))
}

func (h *BackupTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	rowsAffected, err := h.queries.AcquireBackupLock(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire backup lock: %w", err)
	}
	if rowsAffected == 0 {
		return nil, fmt.Errorf("backup lock held — skipping")
	}
	defer func() {
		if _, relErr := h.queries.ReleaseBackupLock(context.Background()); relErr != nil {
			h.logger.Error(nil, "release backup lock: %v", relErr)
		}
	}()

	if err := h.waitForDrain(ctx); err != nil {
		return nil, err
	}

	h.logger.Info(nil, "starting scheduled backup")

	cfg := h.getConfig()
	configPath := filepath.Join(cfg.App.ConfigDir, "config.yaml")

	result, err := backup.Create(ctx, h.db, database.SchemaFS, cfg.Backup.Path, configPath, cfg.Storage.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	if err := backup.ApplyRetention(cfg.Backup.Path, cfg.Backup.Keep); err != nil {
		h.logger.Error(nil, "retention cleanup: %v", err)
	}

	h.logger.Info(nil, "backup completed: %s (%d bytes)", result.Path, result.SizeBytes)

	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}

	return raw, nil
}

func (h *BackupTaskHandler) waitForDrain(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		count, err := h.queries.CountProcessingTasks(ctx)
		if err != nil {
			return fmt.Errorf("count processing tasks: %w", err)
		}
		if count == 0 {
			return nil
		}

		h.logger.Info(nil, "backup: waiting for %d in-flight task(s) to drain", count)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

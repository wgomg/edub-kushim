package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
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
	var p struct {
		Mode string `json:"mode"`
	}
	if len(payload) > 0 {
		json.Unmarshal(payload, &p)
	}
	if p.Mode == "" {
		p.Mode = string(backup.BackupModeFull)
	}
	return fmt.Sprintf("backup:%s:%s", p.Mode, time.Now().UTC().Format("2006-01-02"))
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

	if err := database.WaitForTaskDrain(ctx, h.queries, h.logger, "backup"); err != nil {
		return nil, err
	}

	var payload struct {
		Mode string `json:"mode"`
		Path string `json:"path"`
		Keep int    `json:"keep"`
	}
	if len(t.Payload) > 0 {
		if err := json.Unmarshal(t.Payload, &payload); err != nil {
			return nil, fmt.Errorf("parse backup payload: %w", err)
		}
	}
	mode := backup.BackupMode(payload.Mode)
	if mode == "" {
		mode = backup.BackupModeFull
	}

	cfg := h.getConfig()
	backupDir, err := resolveBackupPath(payload.Path, cfg)
	if err != nil {
		return nil, err
	}
	keep := max(payload.Keep, 0)
	configPath := filepath.Join(cfg.App.ConfigDir, "config.yaml")

	h.logger.Info(nil, "starting scheduled backup (mode=%s)", mode)

	result, err := backup.Create(ctx, h.db, database.SchemaFS, mode, backupDir, configPath, cfg.Storage.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	if err := backup.ApplyRetention(backupDir, mode, keep); err != nil {
		h.logger.Error(nil, "retention cleanup: %v", err)
	}

	h.logger.Info(nil, "backup completed: %s (%d bytes)", result.Path, result.SizeBytes)

	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}

	return raw, nil
}

func resolveBackupPath(payloadPath string, cfg *config.Config) (string, error) {
	if cfg.Backup.Path == "" {
		return "", fmt.Errorf("backup.path is not configured")
	}
	if payloadPath == "" {
		return cfg.Backup.Path, nil
	}
	resolved, err := filepath.Abs(payloadPath)
	if err != nil {
		return "", fmt.Errorf("resolve backup path: %w", err)
	}
	resolved = filepath.Clean(resolved)
	for _, root := range backupRoots(cfg) {
		root = filepath.Clean(root)
		if resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("backup path %q is not within any configured backup root", payloadPath)
}

func backupRoots(cfg *config.Config) []string {
	roots := make([]string, 0, 1+len(cfg.Backup.Schedules))
	if cfg.Backup.Path != "" {
		roots = append(roots, cfg.Backup.Path)
	}
	for _, s := range cfg.Backup.Schedules {
		if s.Path != "" {
			roots = append(roots, s.Path)
		}
	}
	return roots
}

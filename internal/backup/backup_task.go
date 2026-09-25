package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
)

var _ task.Dedupable = (*BackupTaskHandler)(nil)

// BackupTaskHandler creates a backup archive as a blocking task, per the
// payload's mode and schedule, applying retention; the dedup key is the
// mode, so one backup runs at a time per mode.
type BackupTaskHandler struct {
	db        *sql.DB
	queries   *database.Queries
	getConfig func() *config.Config
	logger    *log.Logger
}

// NewBackupTaskHandler wires the handler to the database and the
// dynamic config.
func NewBackupTaskHandler(db *sql.DB, queries *database.Queries, getConfig func() *config.Config, logger *log.Logger) *BackupTaskHandler {
	return &BackupTaskHandler{
		db:        db,
		queries:   queries,
		getConfig: getConfig,
		logger:    logger,
	}
}

// DedupKey is the backup mode, so concurrent backups of one mode
// collapse.
func (h *BackupTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		Mode string `json:"mode"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	if p.Mode == "" {
		p.Mode = string(BackupModeFull)
	}
	return fmt.Sprintf("backup:%s:%s", p.Mode, time.Now().UTC().Format("2006-01-02"))
}

// Handle runs the configured backup schedules that are due, creates their
// archives and applies retention, and returns the run's summary.
func (h *BackupTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	return task.RunBlocking(ctx, h.queries, h.logger, t, "backup", func(progress *task.ProgressTracker) (json.RawMessage, error) {
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
		mode := BackupMode(payload.Mode)
		if mode == "" {
			mode = BackupModeFull
		}

		cfg := h.getConfig()
		backupDir, err := resolveBackupPath(payload.Path, cfg)
		if err != nil {
			return nil, err
		}
		keep := max(payload.Keep, 0)
		configPath := filepath.Join(cfg.App.ConfigDir, "config.yaml")

		h.logger.Info("", "starting scheduled backup (mode=%s)", mode)

		progress.Set("snapshot", fmt.Sprintf("%s → %s", mode, backupDir), 0)
		result, err := Create(ctx, h.db, database.SchemaFS, mode, backupDir, configPath, cfg.Storage.StorageDir)
		if err != nil {
			return nil, fmt.Errorf("backup failed: %w", err)
		}

		progress.Set("retention", "", 0)
		if err := ApplyRetention(backupDir, mode, keep); err != nil {
			h.logger.Error("", "retention cleanup: %v", err)
		}

		h.logger.Info("", "backup completed: %s (%d bytes)", result.Path, result.SizeBytes)

		raw, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshal result: %w", err)
		}

		return raw, nil
	})
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

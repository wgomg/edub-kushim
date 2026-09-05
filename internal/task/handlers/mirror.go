package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/mirror"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
	"github.com/wgomg/edub-kushim/internal/version"
)

var _ task.Dedupable = (*MirrorTaskHandler)(nil)

type MirrorTaskHandler struct {
	queries   *database.Queries
	getConfig func() *config.Config
	logger    *utils.Logger
}

func NewMirrorTaskHandler(queries *database.Queries, getConfig func() *config.Config, logger *utils.Logger) *MirrorTaskHandler {
	return &MirrorTaskHandler{
		queries:   queries,
		getConfig: getConfig,
		logger:    logger,
	}
}

func (h *MirrorTaskHandler) DedupKey(payload json.RawMessage) string {
	return fmt.Sprintf("mirror:%s", time.Now().UTC().Format("2006-01-02"))
}

func (h *MirrorTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
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

	var payload struct {
		Path string `json:"path"`
	}
	if len(t.Payload) > 0 {
		if err := json.Unmarshal(t.Payload, &payload); err != nil {
			return nil, fmt.Errorf("parse mirror payload: %w", err)
		}
	}

	cfg := h.getConfig()
	dest := cfg.Mirror.Path
	if payload.Path != "" {
		dest = payload.Path
	}
	if dest == "" {
		return nil, fmt.Errorf("mirror.path is not configured")
	}

	if err := mirror.ValidateDestination(dest, cfg.Storage.StorageDir, cfg.Backup.Path); err != nil {
		return nil, err
	}

	result, timestamp, err := mirror.RunLocked(ctx, h.queries, h.logger, cfg.Storage.StorageDir, dest)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(map[string]any{
		"path":      dest,
		"files":     result.Files,
		"bytes":     result.Bytes,
		"timestamp": timestamp,
		"version":   version.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}

	return raw, nil
}

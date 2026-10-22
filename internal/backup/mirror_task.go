package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/version"
)

// MirrorTaskHandler mirrors the storage directory as a blocking task;
// its dedup key is the UTC day, one mirror at most per day.
type MirrorTaskHandler struct {
	queries   *database.Queries
	getConfig func() *config.Config
	logger    *log.Logger
}

// NewMirrorTaskHandler wires the handler to the queries and the dynamic
// config.
func NewMirrorTaskHandler(queries *database.Queries, getConfig func() *config.Config, logger *log.Logger) *MirrorTaskHandler {
	return &MirrorTaskHandler{
		queries:   queries,
		getConfig: getConfig,
		logger:    logger,
	}
}

// MirrorPayload is the enqueued mirror task's payload: the destination override, the configured interval
// plus the timing of the schedule that triggered the run.
type MirrorPayload struct {
	Path     string         `json:"path"`
	Interval float64        `json:"interval"`
	Schedule ScheduleTiming `json:"schedule"`
}

// Handle mirrors under the maintenance lock, waiting for the lock while
// reporting the queue depth, and returns the mirror stats as the result.
func (h *MirrorTaskHandler) Handle(ctx context.Context, t task.Task) (out json.RawMessage, err error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)
	defer func() {
		task.FinalizeBatchStatus(ctx, h.queries, h.logger, t.BatchID, err != nil)
	}()

	task.MarkBatchProcessing(ctx, h.queries, h.logger, t.BatchID)

	var payload MirrorPayload
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

	if err := ValidateDestination(dest, cfg.Storage.StorageDir, cfg.Backup.Path); err != nil {
		return nil, err
	}

	result, timestamp, err := RunLocked(ctx, h.queries, h.logger, cfg.Storage.StorageDir, dest,
		func(count int64) { progress.Set("drain-wait", fmt.Sprintf("%d in-flight", count), 0) },
		func() { progress.Set("sync", dest, 0) },
		uuid.NullUUID{UUID: t.ClaimToken, Valid: true},
	)
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

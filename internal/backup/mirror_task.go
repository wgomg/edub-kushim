package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
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

// MirrorDedupKey derives the mirror task's dedup key from the UTC day: one mirror per day.
func MirrorDedupKey(json.RawMessage) string {
	return fmt.Sprintf("mirror:%s", time.Now().UTC().Format(dedupKeyDateLayout))
}

// MirrorSpecs returns the single mirror task a request enqueues; the caller supplies the origin spec.
func MirrorSpecs(path string, interval float64, timing ScheduleTiming) ([]task.TaskSpec, error) {
	payload, err := json.Marshal(MirrorPayload{
		Path:     path,
		Interval: interval,
		Schedule: timing,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal mirror payload: %w", err)
	}
	return []task.TaskSpec{{Type: types.Task.Type.Mirror, Payload: payload}}, nil
}

// Handle mirrors the storage tree and returns the mirror stats as the result. The runner owns the claimed
// maintenance lock, its heartbeat and the drain of the waiting tasks.
func (h *MirrorTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)

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

	result, timestamp, err := RunMirrorAndRecord(ctx, h.logger, cfg.Storage.StorageDir, dest,
		func() { progress.Set("sync", dest, 0) })
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

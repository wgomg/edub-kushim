package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/storage"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ThumbnailTaskHandler struct {
	runner  *tools.Runner
	queries *database.Queries
	logger  *utils.Logger
	getCfg  func() *config.Config
}

func NewThumbnailTaskHandler(runner *tools.Runner, queries *database.Queries, logger *utils.Logger, getCfg func() *config.Config) *ThumbnailTaskHandler {
	return &ThumbnailTaskHandler{runner: runner, queries: queries, logger: logger, getCfg: getCfg}
}

func (h *ThumbnailTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var p struct {
		DocumentID  string `json:"document_id"`
		StoragePath string `json:"storage_path"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal thumbnail payload: %w", err)
	}
	if p.DocumentID == "" {
		return nil, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("thumbnail task %s has no document_id in payload", t.TaskID)}
	}
	if p.StoragePath == "" {
		return nil, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("thumbnail task %s has no storage_path in payload", t.TaskID)}
	}

	document, err := h.queries.GetDocument(ctx, p.DocumentID)
	if err != nil {
		return nil, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("document %s not found", p.DocumentID)}
	}

	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("thumb_%s.jpg", t.TaskID))
	result, err := h.runner.GenerateThumbnail(ctx, p.DocumentID, p.StoragePath, tmpPath)
	if err != nil {
		return nil, &task.Error{ReqID: p.DocumentID, Err: err}
	}

	thumbPath := storage.ThumbnailPath(h.getCfg().Storage.StorageDir, document.CreatedAt.Time, p.DocumentID)
	if err := consumption.MoveFile(tmpPath, thumbPath); err != nil {
		os.Remove(tmpPath)
		return nil, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("move thumbnail into storage: %w", err)}
	}

	if err := h.queries.SetDocumentHasThumbnail(ctx, p.DocumentID); err != nil {
		return nil, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("mark document has_thumbnail: %w", err)}
	}

	h.logger.Info(&p.DocumentID, "thumbnail generated for %s -> %s (%dx%d)", p.StoragePath, thumbPath, result.Width, result.Height)

	raw, _ := json.Marshal(map[string]any{
		"document_id": p.DocumentID,
		"width":       result.Width,
		"height":      result.Height,
	})
	return raw, nil
}

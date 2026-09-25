package consumption

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// ThumbnailTaskHandler generates one document's first-page thumbnail into
// the storage layout's bucketed thumbnail path, using the thumbnail
// configuration of the moment.
type ThumbnailTaskHandler struct {
	runner  *tools.Runner
	queries *database.Queries
	logger  *log.Logger
	getCfg  func() *config.Config
}

// NewThumbnailTaskHandler wires the handler to the runner, the queries,
// the logger and the dynamic config.
func NewThumbnailTaskHandler(runner *tools.Runner, queries *database.Queries, logger *log.Logger, getCfg func() *config.Config) *ThumbnailTaskHandler {
	return &ThumbnailTaskHandler{runner: runner, queries: queries, logger: logger, getCfg: getCfg}
}

// Handle renders the payload document's thumbnail into a temp file, moves
// it into the layout, and marks the document row; the document must exist
// when the task runs.
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
	progress := task.NewProgressTracker(h.queries, t.TaskID)
	progress.Set("render", "", 0)
	result, err := h.runner.GenerateThumbnail(ctx, p.DocumentID, p.StoragePath, tmpPath)
	if err != nil {
		return nil, &task.Error{ReqID: p.DocumentID, Err: err}
	}

	progress.Set("save", "", 0)
	thumbPath := storage.ThumbnailPath(h.getCfg().Storage.StorageDir, document.CreatedAt.Time, p.DocumentID)
	if err := MoveFile(tmpPath, thumbPath); err != nil {
		_ = os.Remove(tmpPath)
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

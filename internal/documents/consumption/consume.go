package consumption

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
)

// ConsumeTaskHandler consumes one file as a task: it runs the consumer's Process and returns the document's
// storage path and ids as the task result. The waiting successors linked to the task are activated, skipped
// or discarded by the task system when the task completes or fails, so the handler carries no linkage,
// child-payload or child-dedup code.
type ConsumeTaskHandler struct {
	consumer *Consumer
	queries  *database.Queries
	logger   *log.Logger
}

// NewConsumeTaskHandler wires the handler to the consumer and the queries its progress tracker writes
// through.
func NewConsumeTaskHandler(consumer *Consumer, queries *database.Queries, logger *log.Logger) *ConsumeTaskHandler {
	return &ConsumeTaskHandler{consumer: consumer, queries: queries, logger: logger}
}

// Handle consumes the payload's file and returns the document's storage path and ids as the task result; a
// failure is reported to the runner, which fails the task and discards its waiting branch with the error.
func (h *ConsumeTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)

	var p struct {
		FilePath   string `json:"file_path"`
		DocumentID string `json:"document_id"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	if p.FilePath == "" {
		return nil, h.documentError(p.DocumentID, fmt.Errorf("task %s has no file_path in payload", t.TaskID))
	}

	file, err := FileFromPath(p.FilePath)
	if err != nil {
		return nil, h.documentError(p.DocumentID, fmt.Errorf("build file from path: %w", err))
	}

	file, err = h.consumer.Process(ctx, file, p.DocumentID, progress.Set)
	{
		mem := log.ReadMemFull()
		h.logger.Debug(p.DocumentID, "post-consume memory: %s", log.FormatMemFull(mem))
	}
	if err != nil {
		return nil, h.documentError(p.DocumentID, err)
	}

	result := struct {
		DocumentDbId int64  `json:"document_db_id"`
		StoragePath  string `json:"storage_path"`
		DocumentID   string `json:"document_id"`
	}{
		DocumentDbId: file.DocumentDbId.Int64,
		StoragePath:  *file.StorageProcessedPath,
		DocumentID:   p.DocumentID,
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return raw, nil
}

// documentError carries the document id as the request id when the payload names one, so log correlation
// survives a failure that happens before the document row exists.
func (h *ConsumeTaskHandler) documentError(documentID string, err error) error {
	if documentID == "" {
		return err
	}
	return &task.Error{ReqID: documentID, Err: err}
}

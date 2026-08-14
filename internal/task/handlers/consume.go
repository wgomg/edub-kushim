package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ConsumeTaskHandler struct {
	consumer *consumption.Consumer
	store    *task.Store
	logger   *utils.Logger
}

func NewConsumeTaskHandler(consumer *consumption.Consumer, store *task.Store, logger *utils.Logger) *ConsumeTaskHandler {
	return &ConsumeTaskHandler{
		consumer: consumer,
		store:    store,
		logger:   logger,
	}
}

func (h *ConsumeTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var p struct {
		FilePath             string `json:"file_path"`
		DocumentID           string `json:"document_id"`
		OnCompleted          string `json:"on_completed"`
		OnCompletedThumbnail string `json:"on_completed_thumbnail"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		onCompleted, onCompletedThumbnail := h.recoverOnCompleted(t.Payload)
		return nil, h.withDiscardAttempt(ctx, t, onCompleted, onCompletedThumbnail, fmt.Errorf("unmarshal payload: %w", err))
	}
	if p.FilePath == "" {
		if p.DocumentID != "" {
			return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, p.OnCompletedThumbnail, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("task %s has no file_path in payload", t.TaskID)})
		}
		return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, p.OnCompletedThumbnail, fmt.Errorf("task %s has no file_path in payload", t.TaskID))
	}

	file, err := consumption.FileFromPath(p.FilePath)
	if err != nil {
		if p.DocumentID != "" {
			return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, p.OnCompletedThumbnail, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("build file from path: %w", err)})
		}
		return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, p.OnCompletedThumbnail, fmt.Errorf("build file from path: %w", err))
	}

	file, err = h.consumer.Process(ctx, file, p.DocumentID)
	{
		mem := utils.ReadMemFull()
		h.logger.Debug(&p.DocumentID, "post-consume memory: %s", utils.FormatMemFull(mem))
	}
	if err != nil {
		return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, p.OnCompletedThumbnail, err)
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

	if p.OnCompleted != "" && file.DocumentDbId.Int64 != 0 {
		if err := h.activateChildEnrich(ctx, t, p.OnCompleted, p.DocumentID); err != nil {
			h.logger.Error(&p.DocumentID, "failed to activate enrich task for consume %s: %v", t.TaskID, err)
		}
	}

	if p.OnCompletedThumbnail != "" && file.DocumentDbId.Int64 != 0 {
		if err := h.activateChildThumbnail(ctx, t, p.OnCompletedThumbnail, p.DocumentID, *file.StorageProcessedPath); err != nil {
			return nil, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("activate thumbnail task: %w", err)}
		}
	}

	return raw, nil
}

func (h *ConsumeTaskHandler) activateChildEnrich(
	ctx context.Context,
	parent task.Task,
	onCompleted, documentID string,
) error {
	enrichTask, err := h.store.GetTaskByTaskID(ctx, onCompleted)
	if err != nil {
		return fmt.Errorf("find waiting enrich task %s: %w", onCompleted, err)
	}
	if enrichTask.Payload == nil {
		return fmt.Errorf("nil payload on enrich task %s", enrichTask.TaskID)
	}

	var enrichPayload struct {
		WaitingFor string `json:"waiting_for"`
	}
	if err := json.Unmarshal(*enrichTask.Payload, &enrichPayload); err != nil {
		return fmt.Errorf("parse enrich payload for %s: %w", enrichTask.TaskID, err)
	}
	if enrichPayload.WaitingFor != parent.TaskID {
		return fmt.Errorf("waiting_for mismatch: enrich %s has waiting_for=%q, expected %q",
			enrichTask.TaskID, enrichPayload.WaitingFor, parent.TaskID)
	}

	var p map[string]any
	if err := json.Unmarshal(*enrichTask.Payload, &p); err != nil {
		return fmt.Errorf("parse enrich payload for update %s: %w", enrichTask.TaskID, err)
	}
	p["document_id"] = documentID
	updatedPayload, _ := json.Marshal(p)

	if err := h.store.SetPending(ctx, enrichTask.ID, updatedPayload); err != nil {
		return fmt.Errorf("activate enrich task %s: %w", enrichTask.TaskID, err)
	}
	return nil
}

func (h *ConsumeTaskHandler) deactivateChildEnrich(
	ctx context.Context,
	parent task.Task,
	onCompleted string,
	parentErr error,
) error {
	enrichTask, err := h.store.GetTaskByTaskID(ctx, onCompleted)
	if err != nil {
		return fmt.Errorf("find waiting enrich task %s: %w", onCompleted, err)
	}
	if enrichTask.Payload == nil {
		return fmt.Errorf("nil payload on enrich task %s", enrichTask.TaskID)
	}

	var enrichPayload struct {
		WaitingFor string `json:"waiting_for"`
	}
	if err := json.Unmarshal(*enrichTask.Payload, &enrichPayload); err != nil {
		return fmt.Errorf("parse enrich payload for %s: %w", enrichTask.TaskID, err)
	}
	if enrichPayload.WaitingFor != parent.TaskID {
		return fmt.Errorf("waiting_for mismatch: enrich %s has waiting_for=%q, expected %q",
			enrichTask.TaskID, enrichPayload.WaitingFor, parent.TaskID)
	}

	rows, err := h.store.Discard(ctx, enrichTask.ID, parentErr.Error())
	if err != nil {
		return fmt.Errorf("discard enrich task %s: %w", enrichTask.TaskID, err)
	}
	if rows == 0 {
		h.logger.Info(nil, "enrich task %s already discarded or activated (consume %s failed)", enrichTask.TaskID, parent.TaskID)
	}
	return nil
}

func (h *ConsumeTaskHandler) activateChildThumbnail(
	ctx context.Context,
	parent task.Task,
	onCompletedThumbnail, documentID, storagePath string,
) error {
	thumbTask, err := h.store.GetTaskByTaskID(ctx, onCompletedThumbnail)
	if err != nil {
		return fmt.Errorf("find waiting thumbnail task %s: %w", onCompletedThumbnail, err)
	}
	if thumbTask.Payload == nil {
		return fmt.Errorf("nil payload on thumbnail task %s", thumbTask.TaskID)
	}

	var thumbPayload struct {
		WaitingFor string `json:"waiting_for"`
	}
	if err := json.Unmarshal(*thumbTask.Payload, &thumbPayload); err != nil {
		return fmt.Errorf("parse thumbnail payload for %s: %w", thumbTask.TaskID, err)
	}
	if thumbPayload.WaitingFor != parent.TaskID {
		return fmt.Errorf("waiting_for mismatch: thumbnail %s has waiting_for=%q, expected %q",
			thumbTask.TaskID, thumbPayload.WaitingFor, parent.TaskID)
	}

	var p map[string]any
	if err := json.Unmarshal(*thumbTask.Payload, &p); err != nil {
		return fmt.Errorf("parse thumbnail payload for update %s: %w", thumbTask.TaskID, err)
	}
	p["document_id"] = documentID
	p["storage_path"] = storagePath
	updatedPayload, _ := json.Marshal(p)

	if err := h.store.SetPending(ctx, thumbTask.ID, updatedPayload); err != nil {
		return fmt.Errorf("activate thumbnail task %s: %w", thumbTask.TaskID, err)
	}
	return nil
}

func (h *ConsumeTaskHandler) deactivateChildThumbnail(
	ctx context.Context,
	parent task.Task,
	onCompletedThumbnail string,
	parentErr error,
) error {
	thumbTask, err := h.store.GetTaskByTaskID(ctx, onCompletedThumbnail)
	if err != nil {
		return fmt.Errorf("find waiting thumbnail task %s: %w", onCompletedThumbnail, err)
	}
	if thumbTask.Payload == nil {
		return fmt.Errorf("nil payload on thumbnail task %s", thumbTask.TaskID)
	}

	var thumbPayload struct {
		WaitingFor string `json:"waiting_for"`
	}
	if err := json.Unmarshal(*thumbTask.Payload, &thumbPayload); err != nil {
		return fmt.Errorf("parse thumbnail payload for %s: %w", thumbTask.TaskID, err)
	}
	if thumbPayload.WaitingFor != parent.TaskID {
		return fmt.Errorf("waiting_for mismatch: thumbnail %s has waiting_for=%q, expected %q",
			thumbTask.TaskID, thumbPayload.WaitingFor, parent.TaskID)
	}

	rows, err := h.store.Discard(ctx, thumbTask.ID, parentErr.Error())
	if err != nil {
		return fmt.Errorf("discard thumbnail task %s: %w", thumbTask.TaskID, err)
	}
	if rows == 0 {
		h.logger.Info(nil, "thumbnail task %s already discarded or activated (consume %s failed)", thumbTask.TaskID, parent.TaskID)
	}
	return nil
}

// withDiscardAttempt attempts to discard the consume task's paired enrich and
// thumbnail tasks on failure; if a discard itself fails, the failure is
// appended to the returned error so it lands in the task's error field.
func (h *ConsumeTaskHandler) withDiscardAttempt(ctx context.Context, t task.Task, onCompleted, onCompletedThumbnail string, err error) error {
	if onCompleted != "" {
		if discardErr := h.deactivateChildEnrich(ctx, t, onCompleted, err); discardErr != nil {
			return fmt.Errorf("%w; additionally failed to discard enrich task %s: %v", err, onCompleted, discardErr)
		}
	}
	if onCompletedThumbnail != "" {
		if discardErr := h.deactivateChildThumbnail(ctx, t, onCompletedThumbnail, err); discardErr != nil {
			return fmt.Errorf("%w; additionally failed to discard thumbnail task %s: %v", err, onCompletedThumbnail, discardErr)
		}
	}
	return err
}

// recoverOnCompleted best-effort extracts on_completed and
// on_completed_thumbnail from a payload that failed strict unmarshalling,
// so the paired enrich and thumbnail tasks can still be discarded.
func (h *ConsumeTaskHandler) recoverOnCompleted(payload json.RawMessage) (onCompleted, onCompletedThumbnail string) {
	var p struct {
		OnCompleted          string `json:"on_completed"`
		OnCompletedThumbnail string `json:"on_completed_thumbnail"`
	}
	json.Unmarshal(payload, &p)
	return p.OnCompleted, p.OnCompletedThumbnail
}

func (h *ConsumeTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		FilePath string `json:"file_path"`
	}
	json.Unmarshal(payload, &p)
	return p.FilePath
}

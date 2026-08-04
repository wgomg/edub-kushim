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
		FilePath    string `json:"file_path"`
		DocumentID  string `json:"document_id"`
		OnCompleted string `json:"on_completed"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, h.withDiscardAttempt(ctx, t, h.recoverOnCompleted(t.Payload), fmt.Errorf("unmarshal payload: %w", err))
	}
	if p.FilePath == "" {
		if p.DocumentID != "" {
			return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("task %s has no file_path in payload", t.TaskID)})
		}
		return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, fmt.Errorf("task %s has no file_path in payload", t.TaskID))
	}

	file, err := consumption.FileFromPath(p.FilePath)
	if err != nil {
		if p.DocumentID != "" {
			return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, &task.Error{ReqID: p.DocumentID, Err: fmt.Errorf("build file from path: %w", err)})
		}
		return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, fmt.Errorf("build file from path: %w", err))
	}

	file, err = h.consumer.Process(ctx, file, p.DocumentID)
	{
		mem := utils.ReadMemFull()
		h.logger.Debug(&p.DocumentID, "post-consume memory: %s", utils.FormatMemFull(mem))
	}
	if err != nil {
		return nil, h.withDiscardAttempt(ctx, t, p.OnCompleted, err)
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

// withDiscardAttempt attempts to discard the consume task's paired enrich task
// on failure; if the discard itself fails, the failure is appended to the
// returned error so it lands in the task's error field.
func (h *ConsumeTaskHandler) withDiscardAttempt(ctx context.Context, t task.Task, onCompleted string, err error) error {
	if onCompleted == "" {
		return err
	}
	if discardErr := h.deactivateChildEnrich(ctx, t, onCompleted, err); discardErr != nil {
		return fmt.Errorf("%w; additionally failed to discard enrich task %s: %v", err, onCompleted, discardErr)
	}
	return err
}

// recoverOnCompleted best-effort extracts on_completed from a payload that
// failed strict unmarshalling, so the paired enrich can still be discarded.
func (h *ConsumeTaskHandler) recoverOnCompleted(payload json.RawMessage) string {
	var p struct {
		OnCompleted string `json:"on_completed"`
	}
	json.Unmarshal(payload, &p)
	return p.OnCompleted
}

func (h *ConsumeTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		FilePath string `json:"file_path"`
	}
	json.Unmarshal(payload, &p)
	return p.FilePath
}

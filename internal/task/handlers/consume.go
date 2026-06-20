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
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	if p.FilePath == "" {
		return nil, fmt.Errorf("task %s has no file_path in payload", t.TaskID)
	}

	file, err := consumption.FileFromPath(p.FilePath)
	if err != nil {
		return nil, fmt.Errorf("build file from path: %w", err)
	}

	file, err = h.consumer.Process(ctx, file, p.DocumentID)
	if err != nil {
		if p.OnCompleted != "" {
			if discardErr := h.deactivateChildEnrich(ctx, t, p.OnCompleted, err); discardErr != nil {
				h.logger.Error(&p.DocumentID, "failed to discard enrich task for consume %s: %v", t.TaskID, discardErr)
			}
		}
		return nil, err
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

	var enrichPayload struct {
		WaitingFor string `json:"waiting_for"`
	}
	if err := json.Unmarshal(enrichTask.Payload, &enrichPayload); err != nil {
		return fmt.Errorf("parse enrich payload for %s: %w", enrichTask.TaskID, err)
	}
	if enrichPayload.WaitingFor != parent.TaskID {
		return fmt.Errorf("waiting_for mismatch: enrich %s has waiting_for=%q, expected %q",
			enrichTask.TaskID, enrichPayload.WaitingFor, parent.TaskID)
	}

	var p map[string]any
	if err := json.Unmarshal(enrichTask.Payload, &p); err != nil {
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

	var enrichPayload struct {
		WaitingFor string `json:"waiting_for"`
	}
	if err := json.Unmarshal(enrichTask.Payload, &enrichPayload); err != nil {
		return fmt.Errorf("parse enrich payload for %s: %w", enrichTask.TaskID, err)
	}
	if enrichPayload.WaitingFor != parent.TaskID {
		return fmt.Errorf("waiting_for mismatch: enrich %s has waiting_for=%q, expected %q",
			enrichTask.TaskID, enrichPayload.WaitingFor, parent.TaskID)
	}

	return h.store.Discard(ctx, enrichTask.ID, parentErr.Error())
}

func (h *ConsumeTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		FilePath string `json:"file_path"`
	}
	json.Unmarshal(payload, &p)
	return p.FilePath
}

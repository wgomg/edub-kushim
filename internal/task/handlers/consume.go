package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
)

type ConsumeTaskHandler struct {
	consumer *consumption.Consumer
}

func NewConsumeTaskHandler(consumer *consumption.Consumer) *ConsumeTaskHandler {
	return &ConsumeTaskHandler{consumer: consumer}
}

func (h *ConsumeTaskHandler) Handle(ctx context.Context, t database.Task) (json.RawMessage, error) {
	var p struct {
		FilePath string `json:"file_path"`
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

	file, err = h.consumer.Process(ctx, file)
	if err != nil {
		return nil, err
	}

	result := struct {
		DocumentID  int64  `json:"document_id"`
		StoragePath string `json:"storage_path"`
	}{
		DocumentID:  file.DocumentID.Int64,
		StoragePath: *file.StorageProcessedPath,
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return raw, nil
}

func (h *ConsumeTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		FilePath string `json:"file_path"`
	}
	json.Unmarshal(payload, &p)
	return p.FilePath
}

package types

import (
	"context"
	"encoding/json"
)

// TaskCreator creates tasks; the task dispatcher satisfies it.
type TaskCreator interface {
	CreateTask(ctx context.Context, taskType TaskType, batchID string, payload json.RawMessage,
		taskID string, status TaskStatus, dedupKey string) (string, error)
}

// BatchCreator creates batch rows.
type BatchCreator interface {
	Create(ctx context.Context, id string, source BatchSource, status BatchStatus) error
}

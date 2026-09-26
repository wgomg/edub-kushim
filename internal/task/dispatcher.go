package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Dispatcher enqueues tasks: it validates the task type against the
// registry, derives the dedup key, and creates the task row in the given
// state. It is the seam the API and the CLI use to start work.
type Dispatcher struct {
	registry *Registry
	store    *Store
}

// NewDispatcher wires the enqueue path to the store and the registry.
func NewDispatcher(store *Store, registry *Registry) *Dispatcher {
	return &Dispatcher{
		registry: registry,
		store:    store,
	}
}

// Enqueue creates a task of the given type and returns its task id. An
// empty taskID becomes a fresh UUID and an empty status becomes pending;
// an unknown task type errors, and a dedup key matching existing work
// rejects the creation.
func (d *Dispatcher) Enqueue(ctx context.Context, taskType types.TaskType, batchID string, payload json.RawMessage, taskID string, status ...types.TaskStatus) (string, error) {
	_, err := d.registry.Get(taskType)
	if err != nil {
		return "", err
	}

	dedupKey := d.registry.DedupKey(taskType, payload)

	if taskID == "" {
		taskID = uuid.New().String()
	}
	taskStatus := types.Task.Status.Pending
	if len(status) > 0 && status[0] != "" {
		taskStatus = status[0]
	}
	_, err = d.store.CreateTask(ctx, taskType, batchID, payload, taskID, taskStatus, dedupKey)
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	return taskID, nil
}

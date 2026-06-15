package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Dispatcher struct {
	logger   *utils.Logger
	registry *Registry
	store    *Store
}

func NewDispatcher(logger *utils.Logger, store *Store, registry *Registry) *Dispatcher {
	return &Dispatcher{
		logger:   logger,
		registry: registry,
		store:    store,
	}
}

func (d *Dispatcher) Enqueue(ctx context.Context, taskType, batchID string, payload json.RawMessage, taskID string, status ...string) (string, error) {
	_, err := d.registry.Get(taskType)
	if err != nil {
		return "", err
	}

	dedupKey := d.registry.DedupKey(taskType, payload)

	if taskID == "" {
		taskID = uuid.New().String()
	}
	taskStatus := "pending"
	if len(status) > 0 && status[0] != "" {
		taskStatus = status[0]
	}
	_, err = d.store.CreateTask(ctx, taskType, batchID, payload, taskID, taskStatus, dedupKey)
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	return taskID, nil
}

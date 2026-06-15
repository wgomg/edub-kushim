package task

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/utils"
)

type Runner struct {
	store    *Store
	registry *Registry
	logger   *utils.Logger
}

func NewRunner(store *Store, registry *Registry, logger *utils.Logger) *Runner {
	return &Runner{
		store:    store,
		registry: registry,
		logger:   logger,
	}
}

func (r *Runner) Next(ctx context.Context, taskType string) error {
	task, err := r.store.ClaimNextPending(ctx, taskType)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("claim next pending task: %w", err)
	}

	h, err := r.registry.Get(task.TaskType)
	if err != nil {
		_ = r.store.FailTask(ctx, task.ID, err.Error())
		return nil
	}

	result, err := h.Handle(ctx, Task{
		ID:       task.ID,
		TaskID:   task.TaskID,
		TaskType: task.TaskType,
		Payload:  task.Payload,
	})
	if err != nil {
		_ = r.store.FailTask(ctx, task.ID, err.Error())
		r.logger.Error(nil, "task %s failed: %v", task.TaskID, err)
		return nil
	}

	if err := r.store.CompleteTask(ctx, task.ID, result); err != nil {
		return fmt.Errorf("complete task %d: %w", task.ID, err)
	}

	return nil
}

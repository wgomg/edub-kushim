package consumption

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
)

// ConsumeTaskHandler implements queue.TaskHandler.
// It builds a File from the task's file path and processes it through Consumer.
type ConsumeTaskHandler struct {
	consumer *Consumer
}

func NewConsumeTaskHandler(consumer *Consumer) *ConsumeTaskHandler {
	return &ConsumeTaskHandler{consumer: consumer}
}

func (h *ConsumeTaskHandler) Handle(ctx context.Context, task database.Task) (sql.NullInt64, error) {
	if !task.FilePath.Valid {
		return sql.NullInt64{}, fmt.Errorf("task %s has no file path", task.TaskID)
	}

	file, err := FileFromPath(task.FilePath.String)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("build file from path: %w", err)
	}

	_, err = h.consumer.Process(file)
	if err != nil {
		return sql.NullInt64{}, err
	}

	return sql.NullInt64{}, nil
}

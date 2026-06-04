package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/cache"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/task/handlers"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Dispatcher struct {
	consumer *consumption.Consumer
	enricher *enrichment.Enricher
	logger   *utils.Logger
	queries  *database.Queries
}

func NewDispatcher(cfg *config.Config, logger *utils.Logger, db *sql.DB, embeddingCache *cache.Cache) (*Dispatcher, error) {
	consumer, err := consumption.NewConsumer(cfg, logger, db)
	if err != nil {
		return nil, err
	}

	enricher, err := enrichment.NewEnricher(cfg, logger, db, embeddingCache)
	if err != nil {
		return nil, err
	}

	return &Dispatcher{
		consumer: consumer,
		enricher: enricher,
		logger:   logger,
		queries:  database.NewQueries(db),
	}, nil
}

func (d *Dispatcher) Enqueue(ctx context.Context, taskType, batchID string, payload json.RawMessage) (string, error) {
	h, err := d.getHandler(taskType)
	if err != nil {
		return "", err
	}

	var dedupKey sql.NullString
	if dd, ok := h.(Dedupable); ok {
		key := dd.DedupKey(payload)
		if key != "" {
			dedupKey = sql.NullString{String: key, Valid: true}
		}
	}

	taskID := uuid.New().String()
	_, err = d.queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   taskID,
		TaskType: taskType,
		Status:   "pending",
		BatchID:  sql.NullString{String: batchID, Valid: batchID != ""},
		Payload:  payload,
		DedupKey: dedupKey,
	})
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	return taskID, nil
}

func (d *Dispatcher) Next(ctx context.Context, taskType string) error {
	id, err := d.queries.GetNextPendingTaskOfType(ctx, taskType)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("get next pending task: %w", err)
	}

	rows, err := d.queries.ClaimTask(ctx, id)
	if err != nil {
		return fmt.Errorf("claim task %d: %w", id, err)
	}
	if rows == 0 {
		return nil
	}

	t, err := d.queries.GetTask(ctx, id)
	if err != nil {
		return fmt.Errorf("get task %d: %w", id, err)
	}

	h, err := d.getHandler(t.TaskType)
	if err != nil {
		d.queries.FailTask(ctx, database.FailTaskParams{
			ID:    id,
			Error: sql.NullString{String: err.Error(), Valid: true},
		})
		return nil
	}

	result, err := h.Handle(ctx, t)
	if err != nil {
		d.queries.FailTask(ctx, database.FailTaskParams{
			ID:    id,
			Error: sql.NullString{String: err.Error(), Valid: true},
		})
		d.logger.Error(nil, "task %s failed: %v", t.TaskID, err)
		return nil
	}

	err = d.queries.CompleteTask(ctx, database.CompleteTaskParams{
		ID:     id,
		Result: &result,
	})
	if err != nil {
		return fmt.Errorf("complete task %d: %w", id, err)
	}

	// Enqueue enrichment after the task is fully committed.
	if taskType == "consume" && result != nil {
		var consumeResult struct {
			DocumentID int64 `json:"document_id"`
		}
		json.Unmarshal(result, &consumeResult)

		var consumePayload struct {
			FilePath  string `json:"file_path"`
			FileIndex int    `json:"file_index"`
		}
		json.Unmarshal(t.Payload, &consumePayload)

		if consumeResult.DocumentID != 0 {
			payload, _ := json.Marshal(map[string]any{
				"document_id": consumeResult.DocumentID,
				"file_name":   filepath.Base(consumePayload.FilePath),
				"file_index":  consumePayload.FileIndex,
			})
			if _, err := d.Enqueue(ctx, "enrich", t.BatchID.String, payload); err != nil {
				d.logger.Error(nil, "failed to enqueue enrich task for document %d: %v", consumeResult.DocumentID, err)
			}
		}
	}

	return nil
}

func (d *Dispatcher) getHandler(taskType string) (Handler, error) {
	switch taskType {
	case "consume":
		return handlers.NewConsumeTaskHandler(d.consumer), nil
	case "enrich":
		return handlers.NewEnrichTaskHandler(d.enricher), nil
	default:
		return nil, fmt.Errorf("unknown task type: %q", taskType)
	}
}

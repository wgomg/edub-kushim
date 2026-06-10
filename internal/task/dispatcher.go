package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

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

func (d *Dispatcher) Enqueue(ctx context.Context, taskType, batchID string, payload json.RawMessage, taskID string, status ...string) (string, error) {
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

	if taskID == "" {
		taskID = uuid.New().String()
	}
	taskStatus := "pending"
	if len(status) > 0 && status[0] != "" {
		taskStatus = status[0]
	}
	_, err = d.queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   taskID,
		TaskType: taskType,
		Status:   taskStatus,
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

	// Activate the waiting enrich task before marking consume as completed,
	// so the poll loop never sees a gap where the batch looks finished.
	if taskType == "consume" && result != nil {
		var consumeResult struct {
			DocumentDbId int64  `json:"document_db_id"`
			DocumentID   string `json:"document_id"`
		}
		json.Unmarshal(result, &consumeResult)

		if consumeResult.DocumentDbId != 0 {
			var consumePayload struct {
				OnCompleted string `json:"on_completed"`
			}
			if err := json.Unmarshal(t.Payload, &consumePayload); err != nil || consumePayload.OnCompleted == "" {
				d.logger.Error(&consumeResult.DocumentID, "consume task %s missing on_completed in payload", t.TaskID)
			} else {
				enrichTask, err := d.queries.GetTaskByTaskID(ctx, consumePayload.OnCompleted)
				if err != nil {
					d.logger.Error(&consumeResult.DocumentID, "failed to find waiting enrich task %s for consume %s: %v", consumePayload.OnCompleted, t.TaskID, err)
				} else {
					var enrichPayload struct {
						WaitingFor string `json:"waiting_for"`
					}
					json.Unmarshal(enrichTask.Payload, &enrichPayload)

					if enrichPayload.WaitingFor != t.TaskID {
						d.logger.Error(&consumeResult.DocumentID, "waiting_for mismatch: enrich %s has waiting_for=%q, expected %q", enrichTask.TaskID, enrichPayload.WaitingFor, t.TaskID)
					} else {
						var p map[string]any
						json.Unmarshal(enrichTask.Payload, &p)
						p["document_id"] = consumeResult.DocumentID
						updatedPayload, _ := json.Marshal(p)

						if err := d.setEnrichTaskPending(ctx, enrichTask.ID, updatedPayload); err != nil {
							d.logger.Error(&consumeResult.DocumentID, "failed to activate enrich task %s: %v", enrichTask.TaskID, err)
						}
					}
				}
			}
		}
	}

	err = d.queries.CompleteTask(ctx, database.CompleteTaskParams{
		ID:     id,
		Result: &result,
	})
	if err != nil {
		return fmt.Errorf("complete task %d: %w", id, err)
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

func (d *Dispatcher) setEnrichTaskPending(ctx context.Context, id int64, payload json.RawMessage) error {
	return d.queries.SetEnrichTaskPending(ctx, database.SetEnrichTaskPendingParams{
		ID:      id,
		Payload: payload,
	})
}

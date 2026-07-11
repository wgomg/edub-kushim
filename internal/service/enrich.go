package service

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
)

type ReEnrich struct {
	queries      *database.Queries
	taskCreator  TaskCreator
	batchCreator BatchCreator
}

func NewReEnrich(queries *database.Queries, taskCreator TaskCreator, batchCreator BatchCreator) *ReEnrich {
	return &ReEnrich{
		queries:      queries,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

func (s *ReEnrich) ReEnrich(ctx context.Context, documentUUID string) (batchID string, err error) {
	_, err = s.queries.GetDocument(ctx, documentUUID)
	if err != nil {
		return "", errs.FromDB(err, "get document")
	}

	batchID = uuid.New().String()

	if err := s.batchCreator.Create(ctx, batchID, "reenrich", "queued"); err != nil {
		return "", errs.FromDB(err, "create batch")
	}

	taskID := uuid.New().String()

	payload, _ := json.Marshal(map[string]any{
		"document_id": documentUUID,
	})

	_, err = s.taskCreator.CreateTask(ctx, "enrich", batchID, payload, taskID, "pending", "enrich:doc:"+documentUUID)
	if err != nil {
		return "", errs.FromDB(err, "create enrich task")
	}

	return batchID, nil
}

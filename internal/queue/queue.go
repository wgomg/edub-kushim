package queue

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type BatchCounts struct {
	Pending    int64
	Processing int64
	Completed  int64
	Failed     int64
}

func (b BatchCounts) Total() int64 {
	return b.Pending + b.Processing + b.Completed + b.Failed
}

type Queue struct {
	logger   *utils.Logger
	queries  *database.Queries
	handler  TaskHandler
	workers  int
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func New(logger *utils.Logger, db *sql.DB, workers int, handler TaskHandler) *Queue {
	return &Queue{
		logger:   logger,
		queries:  database.NewQueries(db),
		handler:  handler,
		workers:  workers,
		interval: 2 * time.Second,
		stopCh:   make(chan struct{}),
	}
}

func (q *Queue) Start() {
	for i := range q.workers {
		q.wg.Add(1)
		go q.workerLoop(i)
		q.logger.Info(nil, "worker %d started", i)
	}
}

func (q *Queue) Stop(ctx context.Context) {
	q.stopOnce.Do(func() {
		close(q.stopCh)
	})

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		q.logger.Info(nil, "all workers stopped")
	case <-ctx.Done():
		q.logger.Info(nil, "worker shutdown timed out")
	}
}

func (q *Queue) EnqueueTask(ctx context.Context, taskName string, batchID string, filePath string) (string, error) {
	taskID := uuid.New().String()

	_, err := q.queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   taskID,
		TaskName: taskName,
		Status:   "pending",
		BatchID:  sql.NullString{String: batchID, Valid: batchID != ""},
		FilePath: sql.NullString{String: filePath, Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	return taskID, nil
}

func (q *Queue) EnqueueFilePaths(ctx context.Context, taskName, batchID string, paths []string) int {
	enqueued := 0
	for _, path := range paths {
		_, err := q.EnqueueTask(ctx, taskName, batchID, path)
		if err != nil {
			q.logger.Error(nil, "enqueue %s: %v", path, err)
			continue
		}
		enqueued++
	}
	return enqueued
}

func (q *Queue) BatchCounts(ctx context.Context, batchID string) BatchCounts {
	return CountBatchStatuses(ctx, q.queries, batchID)
}

// CountBatchStatuses queries the database for per-status counts of tasks in a batch.
// It is a standalone helper so that non-Queue callers (e.g. HTTP handlers) can reuse it.
func CountBatchStatuses(ctx context.Context, queries *database.Queries, batchID string) BatchCounts {
	batchParam := sql.NullString{String: batchID, Valid: true}

	var c BatchCounts
	c.Pending, _ = queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
		BatchID: batchParam,
		Status:  "pending",
	})
	c.Processing, _ = queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
		BatchID: batchParam,
		Status:  "processing",
	})
	c.Completed, _ = queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
		BatchID: batchParam,
		Status:  "completed",
	})
	c.Failed, _ = queries.CountTasksByBatchAndStatus(ctx, database.CountTasksByBatchAndStatusParams{
		BatchID: batchParam,
		Status:  "failed",
	})

	return c
}

func (q *Queue) workerLoop(id int) {
	defer q.wg.Done()
	logPrefix := fmt.Sprintf("[worker %d]", id)

	for {
		select {
		case <-q.stopCh:
			q.logger.Info(nil, "%s stopping", logPrefix)
			return
		case <-time.After(q.interval):
			q.processNext(logPrefix)
		}
	}
}

func (q *Queue) processNext(logPrefix string) {
	ctx := context.Background()

	taskID, err := q.queries.GetNextPendingTask(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return
		}
		q.logger.Error(nil, "%s get next task: %v", logPrefix, err)
		return
	}

	claimed, err := q.queries.ClaimTask(ctx, taskID)
	if err != nil {
		q.logger.Error(nil, "%s claim task %d: %v", logPrefix, taskID, err)
		return
	}
	if claimed == 0 {
		return
	}

	task, err := q.queries.GetTask(ctx, taskID)
	if err != nil {
		q.logger.Error(nil, "%s fetch task %d: %v", logPrefix, taskID, err)
		return
	}

	q.logger.Info(nil, "%s processing task %s (%s)", logPrefix, task.TaskID, task.TaskName)

	docID, err := q.handler.Handle(ctx, task)
	if err != nil {
		q.logger.Error(nil, "%s task %s failed: %v", logPrefix, task.TaskID, err)
		if failErr := q.queries.FailTask(ctx, database.FailTaskParams{
			Error: sql.NullString{String: err.Error(), Valid: true},
			ID:    task.ID,
		}); failErr != nil {
			q.logger.Error(nil, "%s fail task %s: %v", logPrefix, task.TaskID, failErr)
		}
		return
	}

	if err := q.queries.CompleteTask(ctx, database.CompleteTaskParams{
		DocumentID: docID,
		ID:         task.ID,
	}); err != nil {
		q.logger.Error(nil, "%s complete task %s: %v", logPrefix, task.TaskID, err)
	}

	q.logger.Info(nil, "%s task %s completed", logPrefix, task.TaskID)
}

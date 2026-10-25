package enrichment

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// LogBatchBookend adds one INFO line with the aggregated per-document enrich counts over the batch's
// completed enrich tasks, complementing the batch settle line. A failed listing is logged, not returned:
// the bookend is observability, not lifecycle.
func LogBatchBookend(ctx context.Context, queries *database.Queries, logger *log.Logger, batchID string, status types.BatchStatus) {
	tasks, err := task.ListFiltered(ctx, queries, task.TaskFilter{BatchID: batchID})
	if err != nil {
		logger.Error("", "batch %s enrich bookend: list tasks: %v", batchID, err)
		return
	}

	var totals DocumentCounts
	documents := 0
	for _, t := range tasks {
		if t.TaskType != types.Task.Type.Enrich || t.Status != types.Task.Status.Completed || t.Result == nil {
			continue
		}
		counts, ok := CountsFromResult(*t.Result)
		if !ok {
			continue
		}
		documents++
		totals.Add(counts)
	}
	if documents == 0 {
		return
	}

	logger.Info("", "batch %s (%s) enrich: %d documents, %d assignments, %d births, %d promotions, %d rejects, %d defers, %d splits, %d keeps, %d drops",
		batchID, status, documents, totals.Assignments, totals.Births, totals.Promotions, totals.Rejects, totals.Defers, totals.Splits, totals.Keeps, totals.Drops)
}

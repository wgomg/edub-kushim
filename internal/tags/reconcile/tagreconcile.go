package reconcile

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
)

// ReportPrefix marks the serialized Report inside the tagreconcile command's output; the task handler
// extracts the JSON that follows it.
const ReportPrefix = "tagreconcile-report: "

// TagReconcileTaskHandler runs the reconcile pass as a blocking task: it forks the tagreconcile verb
// and extracts its JSON report. The task deduplicates on a fixed key, so concurrent triggers collapse
// into one sweep.
type TagReconcileTaskHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

// NewTagReconcileTaskHandler wires the handler to the queries and logger.
func NewTagReconcileTaskHandler(queries *database.Queries, logger *log.Logger) *TagReconcileTaskHandler {
	return &TagReconcileTaskHandler{queries: queries, logger: logger}
}

// Handle forks the tagreconcile verb for the task's batch and returns its report as the task result.
func (h *TagReconcileTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	return task.RunBlocking(ctx, h.queries, h.logger, t, "tag reconcile", func(progress *task.ProgressTracker) (json.RawMessage, error) {
		progress.Set("run", "tagreconcile --run "+t.BatchID, 0)
		result, err := tags.ForkAndExtractReport("tagreconcile", t.BatchID, ReportPrefix, "tag reconcile")
		if err != nil {
			return nil, err
		}

		if report, parseErr := parseReport(result); parseErr == nil {
			h.logger.Info("", "tag reconcile run: report_only=%t, %d pairs re-checked, %d judged, %d aliases, %d restorations, %d compounds split, %d twin pairs, %d sibling pairs, %d demotions revoked",
				report.ReportOnly, report.Worklist.Stale+report.Worklist.Unjudged+report.Worklist.Drifted+report.Worklist.Aged,
				report.Judged, report.Flips.ToAlias, report.Flips.ToKeep, len(report.CompoundSplits), report.Twins.Worklist,
				report.Siblings.Worklist, report.Demotion.Demoted)
		}
		return result, nil
	})
}

func parseReport(result json.RawMessage) (Report, error) {
	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		return Report{}, err
	}
	return report, nil
}

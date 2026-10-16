package admission

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
)

var _ task.Dedupable = (*TagAuditTaskHandler)(nil)

// TagAuditTaskHandler runs the completeness audit as a blocking task: it forks the tagaudit verb and
// extracts its JSON report. The task deduplicates on a fixed key, so concurrent triggers collapse into one
// run.
type TagAuditTaskHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

// NewTagAuditTaskHandler wires the handler to the queries and logger.
func NewTagAuditTaskHandler(queries *database.Queries, logger *log.Logger) *TagAuditTaskHandler {
	return &TagAuditTaskHandler{queries: queries, logger: logger}
}

// DedupKey is fixed for all tag audit tasks: only one run at a time regardless of payload.
func (h *TagAuditTaskHandler) DedupKey(payload json.RawMessage) string {
	return curation.TagAuditDedupKey
}

// Handle forks the tagaudit verb for the task's batch and returns its report as the task result.
func (h *TagAuditTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	return task.RunBlocking(ctx, h.queries, h.logger, t, "tag audit", func(progress *task.ProgressTracker) (json.RawMessage, error) {
		progress.Set("run", "tagaudit --run "+t.BatchID, 0)
		result, err := tags.ForkAndExtractReport("tagaudit", t.BatchID, AuditReportPrefix, "tag audit")
		if err != nil {
			return nil, err
		}

		var report AuditReport
		if parseErr := json.Unmarshal(result, &report); parseErr == nil {
			h.logger.Info("", "tag audit run %s: sweep=%d sampled=%d flagged=%d fills=%d flags=%d pending_coverage=%d",
				report.RunID, report.SweepCount, report.Sampled, report.Flagged, report.Fills, report.Flags, report.PendingCoverage)
		}
		return result, nil
	})
}

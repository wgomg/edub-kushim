package admission

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
)

// TagIdentityTaskHandler runs the tag identity resolution sweep as a
// blocking task: it forks the tagidentity verb and extracts its JSON
// report. The task deduplicates on a fixed key, so concurrent triggers
// collapse into one sweep.
type TagIdentityTaskHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

// NewTagIdentityTaskHandler wires the handler to the queries and logger.
func NewTagIdentityTaskHandler(queries *database.Queries, logger *log.Logger) *TagIdentityTaskHandler {
	return &TagIdentityTaskHandler{queries: queries, logger: logger}
}

// Handle forks the tagidentity verb for the task's batch and returns its
// report as the task result.
func (h *TagIdentityTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	return task.RunBlocking(ctx, h.queries, h.logger, t, "tag identity", func(progress *task.ProgressTracker) (json.RawMessage, error) {
		progress.Set("run", "tagidentity --run "+t.BatchID, 0)
		result, err := tags.ForkAndExtractReport("tagidentity", t.BatchID, IdentityReportPrefix, "tag identity")
		if err != nil {
			return nil, err
		}

		if report, parseErr := parseIdentityReport(result); parseErr == nil {
			h.logger.Info("", "tag identity run: %d pending, %d resolved, %d folded, %d aliased, %d deferred, %d failed",
				report.Pending, report.Resolved, report.Folded, report.Aliased, report.Deferred, report.Failed)
		}
		return result, nil
	})
}

func parseIdentityReport(result json.RawMessage) (IdentityReport, error) {
	var report IdentityReport
	if err := json.Unmarshal(result, &report); err != nil {
		return IdentityReport{}, err
	}
	return report, nil
}

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
)

var _ task.Dedupable = (*TagIdentityTaskHandler)(nil)

type TagIdentityTaskHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

func NewTagIdentityTaskHandler(queries *database.Queries, logger *log.Logger) *TagIdentityTaskHandler {
	return &TagIdentityTaskHandler{queries: queries, logger: logger}
}

func (h *TagIdentityTaskHandler) DedupKey(payload json.RawMessage) string {
	return "tagidentity"
}

func (h *TagIdentityTaskHandler) Handle(ctx context.Context, t task.Task) (out json.RawMessage, err error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)
	defer func() {
		task.FinalizeBatchStatus(ctx, h.queries, t.BatchID, err != nil)
	}()

	task.MarkBatchProcessing(ctx, h.queries, t.BatchID)

	stopHeartbeat := database.StartMaintenanceLockHeartbeat(ctx, h.queries, h.logger, 5*time.Minute,
		uuid.NullUUID{UUID: t.ClaimToken, Valid: true})
	defer stopHeartbeat()

	if err := database.WaitForTaskDrain(ctx, h.queries, h.logger, "tag identity", func(count int64) {
		progress.Set("drain-wait", fmt.Sprintf("%d in-flight", count), 0)
	}); err != nil {
		return nil, err
	}

	progress.Set("run", "tagidentity --run "+t.BatchID, 0)
	result, err := h.forkAndWait(t.BatchID)
	if err != nil {
		return nil, err
	}

	if report, parseErr := parseIdentityReport(result); parseErr == nil {
		h.logger.Info(nil, "tag identity run: %d pending, %d resolved, %d folded, %d aliased, %d deferred, %d failed",
			report.Pending, report.Resolved, report.Folded, report.Aliased, report.Deferred, report.Failed)
	}
	return result, nil
}

func (h *TagIdentityTaskHandler) forkAndWait(batchID string) (json.RawMessage, error) {
	cmd := exec.Command(os.Args[0], "tagidentity", "--run", batchID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("tag identity run failed: %s", msg)
	}

	result := extractReport(stdout.Bytes(), enrichment.IdentityReportPrefix)
	if result == nil {
		wrapped, _ := json.Marshal(map[string]string{"output": stdout.String()})
		return wrapped, nil
	}
	return result, nil
}

func parseIdentityReport(result json.RawMessage) (enrichment.IdentityReport, error) {
	var report enrichment.IdentityReport
	if err := json.Unmarshal(result, &report); err != nil {
		return enrichment.IdentityReport{}, err
	}
	return report, nil
}

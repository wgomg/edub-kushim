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
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
)

var _ task.Dedupable = (*TagMeasureTaskHandler)(nil)

type TagMeasureTaskHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

func NewTagMeasureTaskHandler(queries *database.Queries, logger *log.Logger) *TagMeasureTaskHandler {
	return &TagMeasureTaskHandler{queries: queries, logger: logger}
}

func (h *TagMeasureTaskHandler) DedupKey(payload json.RawMessage) string {
	return "tagmeasure"
}

func (h *TagMeasureTaskHandler) Handle(ctx context.Context, t task.Task) (out json.RawMessage, err error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)
	defer func() {
		task.FinalizeBatchStatus(ctx, h.queries, t.BatchID, err != nil)
	}()

	task.MarkBatchProcessing(ctx, h.queries, t.BatchID)

	stopHeartbeat := database.StartMaintenanceLockHeartbeat(ctx, h.queries, h.logger, 5*time.Minute,
		uuid.NullUUID{UUID: t.ClaimToken, Valid: true})
	defer stopHeartbeat()

	if err := database.WaitForTaskDrain(ctx, h.queries, h.logger, "tag measure", func(count int64) {
		progress.Set("drain-wait", fmt.Sprintf("%d in-flight", count), 0)
	}); err != nil {
		return nil, err
	}

	progress.Set("run", "tagmeasure --run "+t.BatchID, 0)
	result, err := h.forkAndWait(t.BatchID)
	if err != nil {
		return nil, err
	}

	if report, parseErr := parseReport(result); parseErr == nil {
		h.logger.Info(nil, "tag measure run %s: snapshot=%s rule_set=%s tags=%d operators=%v states=%v skipped=%v worklists=%+v",
			report.RunID, report.SnapshotAt.Format(time.RFC3339), report.RuleSetVersion, report.Tags,
			report.Operators, report.States, report.Skipped, report.Worklists)
	}
	return result, nil
}

func (h *TagMeasureTaskHandler) forkAndWait(runID string) (json.RawMessage, error) {
	cmd := exec.Command(os.Args[0], "tagmeasure", "--run", runID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("tag measure run failed: %s", msg)
	}

	result := extractReport(stdout.Bytes(), tags.ReportPrefix)
	if result == nil {
		wrapped, _ := json.Marshal(map[string]string{"output": stdout.String()})
		return wrapped, nil
	}
	return result, nil
}

func extractReport(stdout []byte, prefix string) json.RawMessage {
	for line := range bytes.SplitSeq(stdout, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte(prefix)) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte(prefix)))
		if json.Valid(payload) {
			return payload
		}
	}
	return nil
}

func parseReport(result json.RawMessage) (tags.Report, error) {
	var report tags.Report
	if err := json.Unmarshal(result, &report); err != nil {
		return tags.Report{}, err
	}
	if report.RunID == "" {
		return tags.Report{}, fmt.Errorf("report has no run id")
	}
	return report, nil
}

package tags

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
)

var _ task.Dedupable = (*TagMeasureTaskHandler)(nil)

// TagMeasureTaskHandler runs the measurement sweep as a blocking task: it
// forks the tagmeasure verb and extracts its JSON report. The task
// deduplicates on a fixed key, so concurrent triggers collapse into one
// sweep.
type TagMeasureTaskHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

// NewTagMeasureTaskHandler wires the handler to the queries and logger.
func NewTagMeasureTaskHandler(queries *database.Queries, logger *log.Logger) *TagMeasureTaskHandler {
	return &TagMeasureTaskHandler{queries: queries, logger: logger}
}

// DedupKey is fixed for all tag measure tasks: one sweep runs at a time
// regardless of payload.
func (h *TagMeasureTaskHandler) DedupKey(payload json.RawMessage) string {
	return "tagmeasure"
}

// Handle forks the tagmeasure verb for the task's batch and returns its
// report as the task result.
func (h *TagMeasureTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	return task.RunBlocking(ctx, h.queries, h.logger, t, "tag measure", func(progress *task.ProgressTracker) (json.RawMessage, error) {
		progress.Set("run", "tagmeasure --run "+t.BatchID, 0)
		result, err := ForkAndExtractReport("tagmeasure", t.BatchID, ReportPrefix, "tag measure")
		if err != nil {
			return nil, err
		}

		if report, parseErr := parseReport(result); parseErr == nil {
			h.logger.Info(nil, "tag measure run %s: snapshot=%s rule_set=%s tags=%d operators=%v states=%v skipped=%v worklists=%+v",
				report.RunID, report.SnapshotAt.Format(time.RFC3339), report.RuleSetVersion, report.Tags,
				report.Operators, report.States, report.Skipped, report.Worklists)
		}
		return result, nil
	})
}

// ForkAndExtractReport forks this binary's subcommand with --run runID and
// extracts the report line carrying reportPrefix from its stdout. When the
// child emits no report line, its raw output is wrapped as {"output": ...} so
// the task payload is never empty. label names the task type in the error.
func ForkAndExtractReport(subcommand, runID, reportPrefix, label string) (json.RawMessage, error) {
	cmd := exec.Command(os.Args[0], subcommand, "--run", runID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s run failed: %s", label, msg)
	}

	result := ExtractReport(stdout.Bytes(), reportPrefix)
	if result == nil {
		wrapped, _ := json.Marshal(map[string]string{"output": stdout.String()})
		return wrapped, nil
	}
	return result, nil
}

// ExtractReport returns the JSON payload following the first stdout line
// that carries the prefix, nil when no valid report line is present.
func ExtractReport(stdout []byte, prefix string) json.RawMessage {
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

func parseReport(result json.RawMessage) (Report, error) {
	var report Report
	if err := json.Unmarshal(result, &report); err != nil {
		return Report{}, err
	}
	if report.RunID == "" {
		return Report{}, fmt.Errorf("report has no run id")
	}
	return report, nil
}

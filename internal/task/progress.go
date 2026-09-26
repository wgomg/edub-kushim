package task

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
)

// ProgressFunc is the callback handlers use to report step progress; the
// tracker persists it onto the task row.
type ProgressFunc func(step, detail string, pct float64)

// ProgressSnapshot is the JSONB shape stored in task.progress. The tracker
// writes it and the API/CLI readers decode it, so the schema lives here.
type ProgressSnapshot struct {
	Step      string  `json:"step"`
	Detail    string  `json:"detail,omitempty"`
	Pct       float64 `json:"pct,omitempty"`
	UpdatedAt string  `json:"updated_at"`
}

// NoopProgress is a ProgressFunc that reports nothing; callers with no
// progress to report pass it where a callback is required.
func NoopProgress(step, detail string, pct float64) {}

// ProgressTracker persists progress snapshots onto a task row, throttled
// to one write per step per second. Writes are best-effort: a failed write
// is dropped and never fails the task.
type ProgressTracker struct {
	queries   *database.Queries
	taskID    string
	mu        sync.Mutex
	lastStep  string
	lastWrite time.Time
}

// NewProgressTracker tracks one task's progress.
func NewProgressTracker(queries *database.Queries, taskID string) *ProgressTracker {
	return &ProgressTracker{queries: queries, taskID: taskID}
}

// Set records the current step, detail and percentage; the stored snapshot
// is what the API and CLI display.
func (p *ProgressTracker) Set(step, detail string, pct float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if step == p.lastStep && now.Sub(p.lastWrite) < time.Second {
		return
	}
	p.lastStep = step
	p.lastWrite = now

	progress := ProgressSnapshot{
		Step:      step,
		Detail:    detail,
		Pct:       pct,
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(progress)
	rawMsg := json.RawMessage(raw)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.queries.UpdateTaskProgress(ctx, database.UpdateTaskProgressParams{
		TaskID:   p.taskID,
		Progress: &rawMsg,
	})
}

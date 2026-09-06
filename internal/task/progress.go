package task

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
)

type ProgressFunc func(step, detail string, pct float64)

// ProgressSnapshot is the JSONB shape stored in task.progress. The tracker
// writes it and the API/CLI readers decode it, so the schema lives here.
type ProgressSnapshot struct {
	Step      string  `json:"step"`
	Detail    string  `json:"detail,omitempty"`
	Pct       float64 `json:"pct,omitempty"`
	UpdatedAt string  `json:"updated_at"`
}

func Progress(progress []ProgressFunc) ProgressFunc {
	if len(progress) > 0 && progress[0] != nil {
		return progress[0]
	}
	return func(string, string, float64) {}
}

type ProgressTracker struct {
	queries   *database.Queries
	taskID    string
	mu        sync.Mutex
	lastStep  string
	lastWrite time.Time
}

func NewProgressTracker(queries *database.Queries, taskID string) *ProgressTracker {
	return &ProgressTracker{queries: queries, taskID: taskID}
}

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
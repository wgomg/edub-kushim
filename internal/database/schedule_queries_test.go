package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestScheduleQueries pins the scheduler's schedule-name keyed reads: the active count covers only the
// queued/processing batches of that name, and the due-ness anchor reads only the latest terminal batch
// of that name — other schedules' history and every non-terminal state stay invisible.
func TestScheduleQueries(t *testing.T) {
	q, db := NewTestQueries(t)
	defer db.Close()
	resetDB(t, q)
	ctx := context.Background()

	if n, err := q.CountActiveBatchesForSchedule(ctx, "sched-a"); err != nil || n != 0 {
		t.Fatalf("empty active count = %d, %v", n, err)
	}
	if _, err := q.GetLastTerminalBatchCreatedAtForSchedule(ctx, "sched-a"); err != sql.ErrNoRows {
		t.Fatalf("empty due-ness anchor: want ErrNoRows, got %v", err)
	}

	insertBatch := func(id, status, schedule string, createdAt time.Time) {
		t.Helper()
		if _, err := q.db.ExecContext(ctx,
			"INSERT INTO batch (id, origin, schedule, status, created_at) VALUES ($1, 'schedule', $2, $3, $4)",
			id, schedule, status, createdAt); err != nil {
			t.Fatalf("insert batch %s: %v", id, err)
		}
	}

	insertBatch("a-queued", "queued", "sched-a", time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC))
	insertBatch("a-processing", "processing", "sched-a", time.Date(2026, 1, 10, 10, 1, 0, 0, time.UTC))
	insertBatch("b-queued", "queued", "sched-b", time.Date(2026, 1, 10, 10, 2, 0, 0, time.UTC))
	if n, err := q.CountActiveBatchesForSchedule(ctx, "sched-a"); err != nil || n != 2 {
		t.Fatalf("active count = %d, %v, want 2 (own queued/processing only)", n, err)
	}
	if n, err := q.CountActiveBatchesForSchedule(ctx, "sched-b"); err != nil || n != 1 {
		t.Fatalf("sched-b active count = %d, %v, want 1", n, err)
	}

	// a terminal batch is never active
	insertBatch("a-done", "completed", "sched-a", time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC))
	if n, err := q.CountActiveBatchesForSchedule(ctx, "sched-a"); err != nil || n != 2 {
		t.Fatalf("active count after a terminal batch = %d, %v, want 2", n, err)
	}

	// the anchor is the latest terminal batch of this name: older terminals, a failed one and a paused
	// batch do not win, and another schedule's newer terminal stays invisible
	insertBatch("a-failed", "failed", "sched-a", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	insertBatch("a-newest", "completed", "sched-a", time.Date(2026, 1, 4, 5, 6, 7, 0, time.UTC))
	insertBatch("a-paused", "paused", "sched-a", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC))
	insertBatch("b-done", "completed", "sched-b", time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC))

	anchor, err := q.GetLastTerminalBatchCreatedAtForSchedule(ctx, "sched-a")
	if err != nil {
		t.Fatalf("due-ness anchor: %v", err)
	}
	if want := time.Date(2026, 1, 4, 5, 6, 7, 0, time.UTC); !anchor.Time.Equal(want) {
		t.Errorf("sched-a anchor = %v, want the latest terminal %v", anchor.Time, want)
	}

	bAnchor, err := q.GetLastTerminalBatchCreatedAtForSchedule(ctx, "sched-b")
	if err != nil {
		t.Fatalf("sched-b anchor: %v", err)
	}
	if want := time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC); !bAnchor.Time.Equal(want) {
		t.Errorf("sched-b anchor = %v, want %v (own schedule only)", bAnchor.Time, want)
	}
}

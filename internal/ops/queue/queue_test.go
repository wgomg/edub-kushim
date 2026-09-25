package queue

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

// TestSweepEmptyQueuedBatches guards the zombie-batch sweep: a queued batch
// older than 10 minutes with zero tasks must be marked failed; younger empty
// batches and batches that already own tasks must be left alone. Without this
// guard a zero-task queued batch would block the polling gate forever.
func TestSweepEmptyQueuedBatches(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	ctx := context.Background()

	oldEmptyID := "old-empty-no-tasks"
	youngEmptyID := "young-empty-no-tasks"
	withTasksID := "young-with-tasks"

	insert := func(id string, ageSeconds int, taskCount int) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx,
			`INSERT INTO batch (id, source, status, created_at) VALUES ($1, 'polling', 'queued', now() - make_interval(secs => $2))`,
			id, ageSeconds); err != nil {
			t.Fatalf("insert batch %s: %v", id, err)
		}
		for i := range taskCount {
			if _, err := client.DB().ExecContext(ctx,
				`INSERT INTO task (task_id, task_type, status, batch_id) VALUES ($1, 'consume', 'pending', $2)`,
				fmt.Sprintf("%s-task-%d", id, i), id); err != nil {
				t.Fatalf("insert task for %s: %v", id, err)
			}
		}
	}
	insert(oldEmptyID, 20*60, 0)
	insert(youngEmptyID, 2*60, 0)
	insert(withTasksID, 30*60, 1)

	logger := testutil.NewTestLogger()
	if err := sweepEmptyQueuedBatches(ctx, client, logger); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	got := func(id string) string {
		t.Helper()
		var status string
		err := client.DB().QueryRowContext(ctx, `SELECT status FROM batch WHERE id = $1`, id).Scan(&status)
		if err == sql.ErrNoRows {
			return "<missing>"
		}
		if err != nil {
			t.Fatalf("read batch %s: %v", id, err)
		}
		return status
	}

	if g, w := got(oldEmptyID), "failed"; g != w {
		t.Errorf("old empty batch status = %q, want %q (sweep must reap zero-task zombies)", g, w)
	}
	if g, w := got(youngEmptyID), "queued"; g != w {
		t.Errorf("young empty batch status = %q, want %q (sweep must not touch batches under the age threshold)", g, w)
	}
	if g, w := got(withTasksID), "queued"; g != w {
		t.Errorf("batch with tasks status = %q, want %q (sweep must leave legitimate batches alone)", g, w)
	}
}

// TestScheduleStateNextDueRephase pins the schedule anchor: an unchanged
// schedule counts whole intervals from the last run, an edited schedule
// re-phases to the next occurrence until it succeeds, and only markRun
// advances the anchor.
func TestScheduleStateNextDueRephase(t *testing.T) {
	lastRun := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	steady := backup.ScheduleTiming{Interval: 7, Time: "02:00"}
	edited := backup.ScheduleTiming{Interval: 3, Time: "03:30"}

	state := scheduleState{lastRun: lastRun, runTiming: &steady}

	if got, want := state.nextDue(steady), time.Date(2026, 6, 8, 2, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("unchanged schedule next due = %v, want %v (full interval from the last run)", got, want)
	}
	if state.pendingRephase {
		t.Error("pendingRephase set although the schedule did not change")
	}

	if got, want := state.nextDue(edited), time.Date(2026, 6, 2, 3, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("edited schedule next due = %v, want %v (next occurrence, not the full interval)", got, want)
	}
	if !state.pendingRephase {
		t.Error("pendingRephase not set for the edited schedule")
	}

	if got, want := state.nextDue(steady), time.Date(2026, 6, 8, 2, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("reverted schedule next due = %v, want %v (falls back to the interval rule)", got, want)
	}
	if state.pendingRephase {
		t.Error("pendingRephase not cleared by the reverted schedule")
	}
	if !state.lastRun.Equal(lastRun) {
		t.Errorf("lastRun = %v, want %v (nextDue must not advance the anchor)", state.lastRun, lastRun)
	}

	state.markRun(edited)
	if !state.runTiming.Equal(edited) {
		t.Errorf("runTiming = %+v, want markRun to adopt the edited schedule %+v", state.runTiming, edited)
	}
	if state.pendingRephase {
		t.Error("pendingRephase not cleared by markRun")
	}
	if since := time.Since(state.lastRun); since < 0 || since > 5*time.Second {
		t.Errorf("lastRun = %v, want it advanced to now", state.lastRun)
	}
}

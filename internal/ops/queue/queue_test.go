package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestEnqueueWithNoTasksCreatesNoBatch guards the invariant that replaced the
// zombie-batch sweep: the transactional enqueue can never commit a batch
// without tasks, so an empty task set leaves no batch row behind.
func TestEnqueueWithNoTasksCreatesNoBatch(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()

	enqueuer := task.NewEnqueuer(client, nil)
	spec, err := task.NewBatchSpec(types.Batch.Origin.Polling, "")
	if err != nil {
		t.Fatalf("build spec: %v", err)
	}

	result, err := enqueuer.EnqueueBatch(ctx, spec, nil)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if result.BatchID != "" || len(result.TaskIDs) != 0 {
		t.Fatalf("result = %+v, want nothing created", result)
	}

	var count int64
	if err := client.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM batch`).Scan(&count); err != nil {
		t.Fatalf("count batches: %v", err)
	}
	if count != 0 {
		t.Errorf("batch count = %d, want 0 (a zero-task enqueue must not create a batch)", count)
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

// TestTagIdentityScheduleSpec pins the identity cadence: a daily 03:30 pass no
// config key can move, gated on the tag adjudicator so an install without a judge
// never schedules one while the pool still claims an explicit enqueue.
func TestTagIdentityScheduleSpec(t *testing.T) {
	cfg := &config.Config{}
	cfg.Enricher.TagReconcile.Interval = 7
	cfg.Enricher.TagReconcile.Time = "05:00"

	if got, want := tagIdentitySpec.timing(cfg), (backup.ScheduleTiming{Interval: 1, Time: "03:30"}); got != want {
		t.Errorf("timing = %+v, want %+v: identity's cadence is pinned, not configured", got, want)
	}

	if tagIdentitySpec.enabled(cfg) {
		t.Error("schedule fires while the tag adjudicator is off")
	}
	cfg.Enricher.TagAdjudicator.Enabled = true
	if !tagIdentitySpec.enabled(cfg) {
		t.Error("schedule does not fire while the tag adjudicator is on")
	}
}

package queue

import (
	"context"
	"os"
	"testing"

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

// TestTagIdentityScheduleDefinition pins the identity cadence: a daily 03:30
// pass no config key can move, gated on the tag adjudicator so an install
// without a judge never schedules one while the batched enqueue still accepts
// an explicit run.
func TestTagIdentityScheduleDefinition(t *testing.T) {
	cfg := &config.Config{}
	cfg.Enricher.TagReconcile.Interval = 7
	cfg.Enricher.TagReconcile.Time = "05:00"

	def, ok := findSchedule(schedulesFor(cfg), scheduleTagIdentity)
	if !ok {
		t.Fatal("identity schedule definition missing")
	}

	if got, want := def.timing(cfg), (backup.ScheduleTiming{Interval: 1, Time: "03:30"}); got != want {
		t.Errorf("timing = %+v, want %+v: identity's cadence is pinned, not configured", got, want)
	}

	if def.enabled(cfg) {
		t.Error("schedule fires while the tag adjudicator is off")
	}
	cfg.Enricher.TagAdjudicator.Enabled = true
	if !def.enabled(cfg) {
		t.Error("schedule does not fire while the tag adjudicator is on")
	}
}

func findSchedule(schedules []scheduleDefinition, name string) (scheduleDefinition, bool) {
	for _, def := range schedules {
		if def.name == name {
			return def, true
		}
	}
	return scheduleDefinition{}, false
}

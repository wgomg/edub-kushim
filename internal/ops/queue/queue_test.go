package queue

import (
	"context"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
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

	def, ok := findSchedule(entriesFor(cfg), scheduleTagIdentity)
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

func findSchedule(entries []schedulerEntry, name string) (schedulerEntry, bool) {
	for _, entry := range entries {
		if entry.name == name {
			return entry, true
		}
	}
	return schedulerEntry{}, false
}

// TestStageConfigCoalescesToNewestTree pins the watcher-to-loop handoff: staging replaces a tree the
// loop has not applied yet, never blocks the watcher, and leaves a single poke for the loop.
func TestStageConfigCoalescesToNewestTree(t *testing.T) {
	d := &daemon{reloadCh: make(chan struct{}, 1)}

	first := &config.Config{}
	second := &config.Config{}
	d.stageConfig(first)
	d.stageConfig(second)

	if got := d.stagedCfg.Load(); got != second {
		t.Fatal("a second staged tree must replace the one the loop has not applied yet")
	}
	if n := len(d.reloadCh); n != 1 {
		t.Fatalf("reloadCh holds %d pokes, want 1 (staging must coalesce)", n)
	}
}

// TestApplyConfigWithUnchangedConnectionSwapsTheTree pins the reload's cheap path: without a database
// connection change the new tree reaches every reader and the pending retry is cleared, while the
// client, the batch service and the notification listener stay untouched.
func TestApplyConfigWithUnchangedConnectionSwapsTheTree(t *testing.T) {
	dbSection := config.DatabaseConfig{Host: "localhost", Port: 5432, Database: "edub", SSLMode: "disable"}
	c := container.NewContainerWithDB(&config.Config{Db: dbSection}, testutil.NewTestLogger(), nil)
	d := &daemon{c: c, pendingCfg: &config.Config{}}

	newCfg := &config.Config{Db: dbSection}
	d.applyConfig(context.Background(), newCfg)

	if got := c.GetConfig(); got != newCfg {
		t.Fatal("the reloaded tree must reach the container")
	}
	if d.pendingCfg != nil {
		t.Fatal("a successful apply must clear the pending retry")
	}
	if d.batchSvc != nil {
		t.Fatal("an unchanged connection must not rebuild the batch service")
	}
	if d.notifyCancel != nil {
		t.Fatal("an unchanged connection must not restart the notification listener")
	}
}

// TestApplyConfigFailedReconnectKeepsThePreviousTree pins the retry contract: a database connection
// change whose reconnect fails reverts the snapshot and leaves the new tree pending, so the next
// housekeeping tick retries it instead of the daemon silently running on the old connection.
func TestApplyConfigFailedReconnectKeepsThePreviousTree(t *testing.T) {
	oldCfg := &config.Config{Db: config.DatabaseConfig{Host: "localhost", Port: 5432, Database: "edub"}}
	c := container.NewContainerWithDB(oldCfg, testutil.NewTestLogger(), nil)
	d := &daemon{c: c, sched: newScheduler(context.Background(), testutil.NewTestLogger())}

	changed := &config.Config{Db: config.DatabaseConfig{
		Host: "127.0.0.1", Port: 1, User: "edub", Password: "edub", Database: "edub", SSLMode: "disable",
	}}
	d.applyConfig(context.Background(), changed)

	if got := c.GetConfig(); got != oldCfg {
		t.Fatal("a failed reconnect must keep the previous configuration tree")
	}
	if d.pendingCfg != changed {
		t.Fatal("the rejected tree must stay pending so the next housekeeping tick retries it")
	}
}

// TestQueueConnBudget pins the daemon pool's formula: the effective slot budget (with the below-one
// fallback the dispatcher enforces) plus the daemon's fixed headroom.
func TestQueueConnBudget(t *testing.T) {
	cases := []struct {
		name       string
		configured int
		want       int
	}{
		{"a configured budget passes through", 4, 4 + queueAuxConns},
		{"a zero budget falls back to the dispatcher's two", 0, 2 + queueAuxConns},
		{"a negative budget falls back to the dispatcher's two", -1, 2 + queueAuxConns},
		{"a raised budget raises the pool", 10, 10 + queueAuxConns},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Srv: config.ServerConfig{MaxConcurrentBatches: tc.configured}}
			testutil.AssertEqual(t, queueConnBudget(cfg), tc.want, "daemon pool budget")
		})
	}
}

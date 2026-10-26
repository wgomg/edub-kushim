package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// fullScheduleConfig builds a configuration declaring every schedule the table can carry: the three
// backup modes plus the six fixed schedules.
func fullScheduleConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Backup.Enabled = true
	cfg.Backup.Schedules = []config.BackupSchedule{
		{Mode: "full", Interval: 7, Time: "02:00", Path: "/b/full", Keep: 2},
		{Mode: "database", Interval: 1, Time: "03:00", Path: "/b/database", Keep: 1},
		{Mode: "documents", Interval: 1, Time: "04:00", Path: "/b/documents", Keep: 1},
	}
	return cfg
}

// TestScheduleNamesAreUnique pins one schedule name per definition: due-ness and the active check are
// keyed by the name, so a collision would silently merge two cadences under one anchor.
func TestScheduleNamesAreUnique(t *testing.T) {
	schedules := schedulesFor(fullScheduleConfig())
	seen := make(map[string]bool, len(schedules))
	for _, def := range schedules {
		if seen[def.name] {
			t.Fatalf("duplicate schedule name %q", def.name)
		}
		seen[def.name] = true
	}
	if got, want := len(schedules), 9; got != want {
		t.Fatalf("schedule count = %d, want %d (three backup modes plus the six fixed schedules)", got, want)
	}
}

// TestBackupSchedulesArePerMode pins the backup cadence split: one schedule per configured mode named
// after its dedup-key prefix so the modes keep independent due-ness, and no backup schedule at all
// while the backup section is disabled.
func TestBackupSchedulesArePerMode(t *testing.T) {
	cfg := fullScheduleConfig()
	schedules := schedulesFor(cfg)
	for _, entry := range cfg.Backup.Schedules {
		def, ok := findSchedule(schedules, backupSchedulePrefix+entry.Mode)
		if !ok {
			t.Fatalf("backup schedule %q missing", backupSchedulePrefix+entry.Mode)
		}
		if def.taskType != types.Task.Type.Backup {
			t.Errorf("%s task type = %q, want backup", def.name, def.taskType)
		}
		if got, want := def.timing(cfg), (backup.ScheduleTiming{Interval: entry.Interval, Time: entry.Time}); got != want {
			t.Errorf("%s timing = %+v, want %+v", def.name, got, want)
		}
	}

	disabled := &config.Config{}
	disabled.Backup.Schedules = cfg.Backup.Schedules
	for _, def := range schedulesFor(disabled) {
		if def.taskType == types.Task.Type.Backup {
			t.Fatalf("backup schedule %q declared while backup is disabled", def.name)
		}
	}
}

// TestScheduleProducersMatchDeclaredType pins each producer's task-set contract: the scheduler refuses
// a spec whose type differs from the definition's, so a producer must build exactly its own type.
// The thumbnail-backfill producer reads the documents table and runs under the database gate.
func TestScheduleProducersMatchDeclaredType(t *testing.T) {
	cfg := fullScheduleConfig()
	for _, def := range schedulesFor(cfg) {
		if def.taskType == types.Task.Type.ThumbBackfill {
			continue
		}
		tasks, err := def.produce(context.Background(), cfg, nil)
		if err != nil {
			t.Fatalf("%s produce: %v", def.name, err)
		}
		if len(tasks) == 0 {
			t.Errorf("%s produced no tasks", def.name)
		}
		for _, spec := range tasks {
			if spec.Type != def.taskType {
				t.Errorf("%s produced a %q spec, want %q", def.name, spec.Type, def.taskType)
			}
		}
	}
}

// TestScheduleEnabledGates pins each schedule's config gate: presence in the table is not enough —
// the loop consults the freshly reloaded config flag on every evaluation.
func TestScheduleEnabledGates(t *testing.T) {
	cases := []struct {
		name     string
		schedule string
		mutate   func(*config.Config)
		want     bool
	}{
		{name: "mirror off by default", schedule: scheduleMirror, mutate: func(*config.Config) {}, want: false},
		{name: "mirror enabled", schedule: scheduleMirror, mutate: func(c *config.Config) { c.Mirror.Enabled = true }, want: true},
		{name: "thumbnail disabled", schedule: scheduleThumbnailBackfill, mutate: func(*config.Config) {}, want: false},
		{name: "thumbnail without interval", schedule: scheduleThumbnailBackfill, mutate: func(c *config.Config) { c.Consumer.Thumbnail.Enabled = true }, want: false},
		{name: "thumbnail enabled with interval", schedule: scheduleThumbnailBackfill, mutate: func(c *config.Config) {
			c.Consumer.Thumbnail.Enabled = true
			c.Consumer.Thumbnail.BackfillInterval = 7
		}, want: true},
		{name: "tag reconcile flag", schedule: scheduleTagReconcile, mutate: func(c *config.Config) { c.Enricher.TagReconcile.Enabled = true }, want: true},
		{name: "tag measure flag", schedule: scheduleTagMeasure, mutate: func(c *config.Config) { c.Enricher.TagMeasure.Enabled = true }, want: true},
		{name: "tag audit flag", schedule: scheduleTagAudit, mutate: func(c *config.Config) { c.Enricher.TagAudit.Enabled = true }, want: true},
		{name: "tag identity follows the adjudicator", schedule: scheduleTagIdentity, mutate: func(c *config.Config) { c.Enricher.TagAdjudicator.Enabled = true }, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			tc.mutate(cfg)
			def, ok := findSchedule(schedulesFor(cfg), tc.schedule)
			if !ok {
				t.Fatalf("schedule %q missing from the table", tc.schedule)
			}
			if got := def.enabled(cfg); got != tc.want {
				t.Errorf("enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSchedulerDueDerivesFromTerminalBatches pins the schedule-name due-ness contract: a schedule with
// no terminal batch anchors to its next preferred slot exactly once, a fresh terminal batch — including
// a failed one — defers the next occurrence instead of retrying every tick, and an aged terminal batch
// makes the schedule due again.
func TestSchedulerDueDerivesFromTerminalBatches(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	s := newScheduler(testutil.NewTestLogger())

	due, err := s.due(ctx, client.Queries, "due-first", backup.ScheduleTiming{Interval: 7, Time: "02:00"})
	if err != nil {
		t.Fatalf("first due: %v", err)
	}
	if due {
		t.Fatal("a schedule with no terminal batch must wait for its next slot")
	}
	first, ok := s.firstDue["due-first"]
	if !ok {
		t.Fatal("first-run anchor not recorded")
	}
	if time.Until(first) <= 0 || first.Hour() != 2 || first.Minute() != 0 {
		t.Errorf("first-run anchor = %v, want the next 02:00 slot in the future", first)
	}
	if _, err := s.due(ctx, client.Queries, "due-first", backup.ScheduleTiming{Interval: 7, Time: "02:00"}); err != nil {
		t.Fatalf("second due: %v", err)
	}
	if got := s.firstDue["due-first"]; !got.Equal(first) {
		t.Errorf("first-run anchor slid from %v to %v: it must be computed once", first, got)
	}

	insertBatch := func(id, status, schedule string, createdAt time.Time) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx,
			"INSERT INTO batch (id, origin, schedule, status, created_at) VALUES ($1, 'schedule', $2, $3, $4)",
			id, schedule, status, createdAt); err != nil {
			t.Fatalf("insert batch %s: %v", id, err)
		}
	}

	insertBatch("due-recent", "failed", "due-recent", time.Now().UTC())
	due, err = s.due(ctx, client.Queries, "due-recent", backup.ScheduleTiming{Interval: 1, Time: "00:00"})
	if err != nil {
		t.Fatalf("failed-batch due: %v", err)
	}
	if due {
		t.Error("a fresh failed batch must defer to the next occurrence, not retry every tick")
	}

	insertBatch("due-stale", "completed", "due-stale", time.Now().UTC().Add(-48*time.Hour))
	due, err = s.due(ctx, client.Queries, "due-stale", backup.ScheduleTiming{Interval: 1, Time: "00:00"})
	if err != nil {
		t.Fatalf("aged due: %v", err)
	}
	if !due {
		t.Error("a terminal batch well past its interval must leave the schedule due")
	}
}

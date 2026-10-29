package queue

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
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

// TestScheduleNamesAreUnique pins one schedule name per named entry: due-ness and the active check are
// keyed by the name, so a collision would silently merge two cadences under one anchor. The unnamed
// polling entry carries no name and is the only entry allowed to.
func TestScheduleNamesAreUnique(t *testing.T) {
	entries := entriesFor(fullScheduleConfig())
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if seen[entry.name] {
			t.Fatalf("duplicate schedule name %q", entry.name)
		}
		seen[entry.name] = true
	}
	if got, want := len(entries), 11; got != want {
		t.Fatalf("entry count = %d, want %d (three backup modes plus the seven fixed schedules and polling)", got, want)
	}
}

// TestBackupSchedulesArePerMode pins the backup cadence split: one schedule per configured mode named
// after its mode so the modes keep independent due-ness, and no backup entry at all while the backup
// section is disabled.
func TestBackupSchedulesArePerMode(t *testing.T) {
	cfg := fullScheduleConfig()
	entries := entriesFor(cfg)
	for _, entry := range cfg.Backup.Schedules {
		def, ok := findSchedule(entries, backupSchedulePrefix+entry.Mode)
		if !ok {
			t.Fatalf("backup schedule %q missing", backupSchedulePrefix+entry.Mode)
		}
		if got, want := def.timing(cfg), (backup.ScheduleTiming{Interval: entry.Interval, Time: entry.Time}); got != want {
			t.Errorf("%s timing = %+v, want %+v", def.name, got, want)
		}
	}

	disabled := &config.Config{}
	disabled.Backup.Schedules = cfg.Backup.Schedules
	for _, def := range entriesFor(disabled) {
		if strings.HasPrefix(def.name, backupSchedulePrefix) {
			t.Fatalf("backup schedule %q declared while backup is disabled", def.name)
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
		{name: "tag maintenance flag", schedule: scheduleTagMaintenance, mutate: func(c *config.Config) { c.Enricher.TagMaintenance.Enabled = true }, want: true},
		{name: "reconcile suppressed by the routine", schedule: scheduleTagReconcile, mutate: func(c *config.Config) {
			c.Enricher.TagReconcile.Enabled = true
			c.Enricher.TagMaintenance.Enabled = true
		}, want: false},
		{name: "measure suppressed by the routine", schedule: scheduleTagMeasure, mutate: func(c *config.Config) {
			c.Enricher.TagMeasure.Enabled = true
			c.Enricher.TagMaintenance.Enabled = true
		}, want: false},
		{name: "audit suppressed by the routine", schedule: scheduleTagAudit, mutate: func(c *config.Config) {
			c.Enricher.TagAudit.Enabled = true
			c.Enricher.TagMaintenance.Enabled = true
		}, want: false},
		{name: "identity keeps its cadence under the routine", schedule: scheduleTagIdentity, mutate: func(c *config.Config) {
			c.Enricher.TagAdjudicator.Enabled = true
			c.Enricher.TagMaintenance.Enabled = true
		}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			tc.mutate(cfg)
			def, ok := findSchedule(entriesFor(cfg), tc.schedule)
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
	s := newScheduler(context.Background(), testutil.NewTestLogger())

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

// TestPollingEntryOriginPairing pins the BatchSpec pairing the scheduler boundary relies on: exactly one
// unnamed entry carrying origin polling and declaring no timing, with every named entry carrying origin
// schedule. A mismatch fails NewBatchSpec on every evaluation instead of enqueueing.
func TestPollingEntryOriginPairing(t *testing.T) {
	entries := entriesFor(fullScheduleConfig())
	unnamed := 0
	for _, entry := range entries {
		if entry.name != "" {
			if entry.origin != types.Batch.Origin.Schedule {
				t.Errorf("%s: origin = %q, want schedule", entry.name, entry.origin)
			}
			continue
		}
		unnamed++
		if entry.origin != types.Batch.Origin.Polling {
			t.Errorf("unnamed entry origin = %q, want polling", entry.origin)
		}
		if entry.timing != nil {
			t.Error("unnamed entry declares timing; its due path reads the polling interval instead")
		}
	}
	if unnamed != 1 {
		t.Fatalf("unnamed entries = %d, want exactly 1", unnamed)
	}
}

// TestPollingEntryEnabledGates pins the polling entry's config gate: it never fires with the polling flag
// off, and an all-day active window keeps it enabled once the flag is on.
func TestPollingEntryEnabledGates(t *testing.T) {
	entry := pollingEntry()
	if entry.enabled(&config.Config{}) {
		t.Error("polling fires while the polling config flag is off")
	}

	enabled := &config.Config{}
	enabled.Consumer.Polling.Enabled = true
	if !entry.enabled(enabled) {
		t.Error("polling does not fire while the polling config flag is on")
	}

	enabled.Consumer.Polling.Windows = []config.PollingWindow{{Start: "00:00", End: "24:00"}}
	if !entry.enabled(enabled) {
		t.Error("polling does not fire inside an all-day active window")
	}
}

// TestIntervalDue pins the unnamed entry's cadence: due immediately with no recorded fire, otherwise due
// only once a full configured interval — floored at one minute — has elapsed since the last dispatch.
func TestIntervalDue(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		interval int
		last     time.Time
		want     bool
	}{
		{name: "never fired", interval: 1, want: true},
		{name: "inside the interval", interval: 1, last: now.Add(-59 * time.Second), want: false},
		{name: "interval elapsed", interval: 1, last: now.Add(-time.Minute), want: true},
		{name: "zero interval floors at one minute", interval: 0, last: now.Add(-30 * time.Second), want: false},
		{name: "multi-minute interval pending", interval: 5, last: now.Add(-4 * time.Minute), want: false},
		{name: "multi-minute interval elapsed", interval: 5, last: now.Add(-5 * time.Minute), want: true},
	}
	s := newScheduler(context.Background(), testutil.NewTestLogger())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Consumer.Polling.Interval = tc.interval
			s.lastFire = map[string]time.Time{}
			if !tc.last.IsZero() {
				s.lastFire[""] = tc.last
			}
			if got := s.intervalDue("", cfg, now); got != tc.want {
				t.Errorf("intervalDue = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFireSkipsUnmetPrecondition pins the enqueue-precondition contract: an unmet environment precondition
// skips the occurrence without surfacing an error and creates no batch, instead of making the daemon log a
// hard error on every evaluation.
func TestFireSkipsUnmetPrecondition(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()

	catalog, err := task.NewCatalog(map[types.TaskType]task.TypeWiring{
		types.Task.Type.Backup: {
			DedupKey: task.ConstantDedupKey("backup:precondition-test"),
			Preconditions: []task.Precondition{
				func(context.Context) error { return errors.New("rsync unavailable (test)") },
			},
		},
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	entry := schedulerEntry{
		name:   "precondition-test",
		origin: types.Batch.Origin.Schedule,
		produce: func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error) {
			return []task.TaskSpec{{Type: types.Task.Type.Backup, Payload: json.RawMessage(`{}`)}}, nil
		},
	}

	s := newScheduler(context.Background(), testutil.NewTestLogger())
	if err := s.fire(ctx, client, task.NewEnqueuer(client, catalog), &config.Config{}, entry); err != nil {
		t.Fatalf("fire: an unmet precondition must skip the occurrence, got error: %v", err)
	}

	var batches int64
	if err := client.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM batch").Scan(&batches); err != nil {
		t.Fatalf("count batches: %v", err)
	}
	if batches != 0 {
		t.Errorf("batch count = %d, want 0 (a precondition skip creates nothing)", batches)
	}
}

// TestFirePropagatesProducerError pins that a producer failure is not swallowed: fire returns it so the
// calling loop logs it under the entry's label instead of silently dropping the occurrence.
func TestFirePropagatesProducerError(t *testing.T) {
	want := errors.New("producer failed")
	entry := schedulerEntry{
		name:   "producer-error-test",
		origin: types.Batch.Origin.Schedule,
		produce: func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error) {
			return nil, want
		},
	}

	s := newScheduler(context.Background(), testutil.NewTestLogger())
	err := s.fire(context.Background(), nil, nil, &config.Config{}, entry)
	if !errors.Is(err, want) {
		t.Fatalf("fire error = %v, want it to wrap %v", err, want)
	}
}

// TestThumbnailOccurrenceRefusesWhileInFlight pins the thumbnail backfill's overlap guard: its duplicate
// protection is the active-batch check, which cannot see a batch whose enqueue is still running, so the
// in-flight flag alone must refuse the next occurrence while one is running.
func TestThumbnailOccurrenceRefusesWhileInFlight(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	s := newScheduler(context.Background(), testutil.NewTestLogger())

	// a terminal batch well past its interval makes the named entry due
	_, err := client.DB().ExecContext(ctx,
		"INSERT INTO batch (id, origin, schedule, status, created_at) VALUES ($1, 'schedule', $2, $3, $4)",
		"thumb-overlap-terminal", scheduleThumbnailBackfill, "completed",
		time.Now().UTC().Add(-48*time.Hour))
	testutil.AssertNoError(t, err, "insert terminal batch")

	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var invocations atomic.Int32
	entry := schedulerEntry{
		name:    scheduleThumbnailBackfill,
		origin:  types.Batch.Origin.Schedule,
		enabled: func(*config.Config) bool { return true },
		timing: func(*config.Config) backup.ScheduleTiming {
			return backup.ScheduleTiming{Interval: 1, Time: "00:00"}
		},
		produce: func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error) {
			invocations.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return nil, nil
		},
	}
	enqueuer := task.NewEnqueuer(client, nil)

	if err := s.evaluate(ctx, client, enqueuer, &config.Config{}, entry); err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first occurrence did not start")
	}

	if err := s.evaluate(ctx, client, enqueuer, &config.Config{}, entry); err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	// the running counter is incremented synchronously, so a second spawned job would already be counted
	if got := s.runningJobs.Load(); got != 1 {
		t.Fatalf("running jobs = %d, want 1 — the occurrence in flight must be refused", got)
	}

	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for (s.backfillInFlight.Load() || s.runningJobs.Load() > 0) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.backfillInFlight.Load() {
		t.Fatal("the in-flight flag must clear when the occurrence finishes")
	}
	if got := s.runningJobs.Load(); got != 0 {
		t.Fatalf("running jobs = %d, want 0 after the occurrence finished", got)
	}
	if got := invocations.Load(); got != 1 {
		t.Fatalf("producer ran %d times, want exactly one occurrence", got)
	}
}

// TestStopBackgroundJobsCancelsAndRearms pins the reload drain: a running producer observes cancellation and
// returns, the drain releases its in-flight flag and counter, and a fresh generation is armed so the next
// occurrence can start.
func TestStopBackgroundJobsCancelsAndRearms(t *testing.T) {
	s := newScheduler(context.Background(), testutil.NewTestLogger())
	oldGeneration := s.jobCtx

	started := make(chan struct{})
	s.spawnBackground(&s.scanInFlight, "drain-test", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the producer did not start")
	}

	s.stopBackgroundJobs()

	if oldGeneration.Err() == nil {
		t.Fatal("the drained generation must be canceled")
	}
	if s.jobCtx.Err() != nil {
		t.Fatal("stopBackgroundJobs must arm a fresh generation for the next occurrence")
	}
	if s.scanInFlight.Load() {
		t.Fatal("the in-flight flag must clear when the producer returns")
	}
	if got := s.runningJobs.Load(); got != 0 {
		t.Fatalf("running jobs = %d, want 0 after the drain", got)
	}
}

// Package queue is the queue daemon: it LISTENs for batch and lock notifications, polls as a safety net,
// claims queued batches through the dispatcher — forking each batch's task CLI in batch mode — schedules
// the recurring runs and requeues stale work through the reaper.
package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/dispatch"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

type daemon struct {
	c        *container.Container
	catalog  *task.Catalog
	client   atomic.Pointer[database.Client]
	batchSvc *service.Batch

	notifyCancel context.CancelFunc
	notifyCh     chan struct{}
	dispatcher   *dispatch.Dispatcher

	thumbnailBackfill scheduleState
	tagReconcile      scheduleState
	tagMeasure        scheduleState
	tagIdentity       scheduleState
	tagAudit          scheduleState

	lastStaleTaskClaim time.Time
}

// Run is the kushim queue verb: it runs the daemon loop until signalled, dispatching queued batches,
// scheduling the recurring runs, forking each batch's task-CLI child and reaping stale work.
func Run(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help("Usage: kushim queue [--bg]") {
		return nil
	}

	bgFlag := false
	fp.Bool("--bg", &bgFlag)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	pidFile := filepath.Join(c.GetConfig().App.ConfigDir, "kushim-queue.pid")
	logFile := c.LogFileConfig("queue.log")

	_ = os.MkdirAll(filepath.Dir(logFile.Path), 0755)
	if err := c.GetLogger().SetLogFile(logFile); err != nil {
		c.GetLogger().Error("", "failed to open queue log file: %v", err)
	}

	if bgFlag {
		cmd := exec.Command(os.Args[0], "queue")
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start background queue daemon: %w", err)
		}
		c.GetLogger().Info("", "queue daemon started in background (PID %d)", cmd.Process.Pid)
		return nil
	}

	if data, err := os.ReadFile(pidFile); err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && syscall.Kill(pid, 0) == nil {
			return fmt.Errorf("queue daemon already running (PID %d)", pid)
		}
		c.GetLogger().Info("", "removing stale PID file from process %d", pid)
	}

	if err := os.WriteFile(pidFile, fmt.Appendf(nil, "%d\n", os.Getpid()), 0644); err != nil {
		return fmt.Errorf("write PID file %s: %w", pidFile, err)
	}
	defer os.Remove(pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			c.GetLogger().Info("", "shutting down queue daemon...")
			_ = os.Remove(pidFile)
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	catalog, err := task.NewCatalog(map[types.TaskType]task.DedupKeyFunc{
		types.Task.Type.Consume:       consumption.DedupKey,
		types.Task.Type.Backup:        backup.BackupDedupKey,
		types.Task.Type.Mirror:        backup.MirrorDedupKey,
		types.Task.Type.ThumbBackfill: service.ThumbnailDedupKey,
		types.Task.Type.TagReconcile:  task.ConstantDedupKey(curation.TagReconcileDedupKey),
		types.Task.Type.TagMeasure:    task.ConstantDedupKey(tags.TagMeasureDedupKey),
		types.Task.Type.TagIdentity:   task.ConstantDedupKey(curation.TagIdentityDedupKey),
		types.Task.Type.TagAudit:      task.ConstantDedupKey(curation.TagAuditDedupKey),
	}, nil)
	if err != nil {
		return fmt.Errorf("task catalog: %w", err)
	}

	maxConcurrent := c.GetConfig().Srv.MaxConcurrentBatches
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}

	c.GetLogger().Info("", "queue daemon started (max %d concurrent batches)", maxConcurrent)

	d := &daemon{
		c:        c,
		catalog:  catalog,
		batchSvc: service.NewBatch(client, c.GetConfig().Consumer.Reclaim.MaxRetries),
	}
	d.client.Store(client)

	go d.runPollingLoop(ctx)

	d.startNotify(ctx)
	defer d.stopNotify()

	wake := func() {
		select {
		case d.notifyCh <- struct{}{}:
		default:
		}
	}
	d.dispatcher = dispatch.NewDispatcher(catalog, c.GetLogger(), types.HostQueue, maxConcurrent, wake, nil)

	safetyInterval := 30 * time.Second
	safetyTimer := time.NewTimer(safetyInterval)
	defer safetyTimer.Stop()

	hkTicker := time.NewTicker(5 * time.Second)
	defer hkTicker.Stop()

	for {
		select {
		case <-d.notifyCh:
			if err := d.dispatcher.Dispatch(ctx, d.client.Load(), d.batchSvc); err != nil {
				c.GetLogger().Error("", "queue dispatch: %v", err)
			}
			if !safetyTimer.Stop() {
				select {
				case <-safetyTimer.C:
				default:
				}
			}
			safetyTimer.Reset(safetyInterval)

		case <-safetyTimer.C:
			if err := d.dispatcher.Dispatch(ctx, d.client.Load(), d.batchSvc); err != nil {
				c.GetLogger().Error("", "queue dispatch: %v", err)
			}
			safetyTimer.Reset(safetyInterval)

		case <-hkTicker.C:
			d.tick(ctx)

		case <-ctx.Done():
			c.GetLogger().Info("", "queue daemon stopped")
			return nil
		}
	}
}

func (d *daemon) startNotify(ctx context.Context) {
	notifyCtx, cancel := context.WithCancel(ctx)
	d.notifyCancel = cancel
	d.notifyCh = make(chan struct{}, 4)
	go dispatch.ListenForBatchNotifications(notifyCtx, config.BuildPostgresDSN(d.c.GetConfig().Db), d.notifyCh, d.c.GetLogger())
}

func (d *daemon) stopNotify() {
	if d.notifyCancel != nil {
		d.notifyCancel()
	}
}

func (d *daemon) tick(ctx context.Context) {
	d.reloadConfig(ctx)
	cfg := d.c.GetConfig()
	client := d.client.Load()
	enqueuer := task.NewEnqueuer(client, d.catalog)

	d.ensureBackup(ctx, cfg, client, enqueuer)
	d.ensureMirror(ctx, cfg, client, enqueuer)

	if cfg.Consumer.Thumbnail.Enabled && cfg.Consumer.Thumbnail.BackfillInterval > 0 {
		if err := maybeScheduleAnchored(ctx, d.c, client, enqueuer, cfg, &d.thumbnailBackfill, thumbnailBackfillSpec); err != nil {
			d.c.GetLogger().Error("", "thumbnail backfill scheduling: %v", err)
		}
	}

	d.ensureTagReconcile(ctx, cfg, client, enqueuer)
	d.ensureTagMeasure(ctx, cfg, client, enqueuer)
	d.ensureTagIdentity(ctx, cfg, client, enqueuer)
	d.ensureTagAudit(ctx, cfg, client, enqueuer)

	d.reclaimStale(ctx, cfg)
}

func (d *daemon) reloadConfig(ctx context.Context) {
	newCfg, err := config.Load(d.c.GetConfig().App.ConfigDir)
	if err != nil {
		d.c.GetLogger().Error("", "config reload: %v", err)
		return
	}
	oldCfg := d.c.GetConfig()
	d.c.SetConfig(newCfg)
	if !config.DatabaseConnectionChanged(oldCfg.Db, newCfg.Db) {
		return
	}

	d.c.GetLogger().Info("", "DB config changed, reconnecting...")
	newClient, err := d.c.ReconnectClient()
	if err != nil {
		d.c.GetLogger().Error("", "failed to reconnect to new DB: %v — keeping previous configuration", err)
		d.c.SetConfig(oldCfg)
		return
	}

	d.client.Store(newClient)
	d.batchSvc = service.NewBatch(newClient, d.c.GetConfig().Consumer.Reclaim.MaxRetries)

	d.stopNotify()
	d.startNotify(ctx)
}

func (d *daemon) ensureBackup(ctx context.Context, cfg *config.Config, client *database.Client, enqueuer *task.Enqueuer) {
	if !cfg.Backup.Enabled {
		return
	}
	locked, lockErr := client.Queries.IsLockHeld(ctx, string(types.LockMaintenance))
	if lockErr != nil {
		d.c.GetLogger().Error("", "backup lock check: %v", lockErr)
	} else if locked == 0 {
		if err := maybeScheduleBackup(ctx, d.c, client, enqueuer, cfg); err != nil {
			d.c.GetLogger().Error("", "backup scheduling: %v", err)
		}
	}
}

func (d *daemon) ensureMirror(ctx context.Context, cfg *config.Config, client *database.Client, enqueuer *task.Enqueuer) {
	if !cfg.Mirror.Enabled {
		return
	}
	locked, lockErr := client.Queries.IsLockHeld(ctx, string(types.LockMaintenance))
	if lockErr != nil {
		d.c.GetLogger().Error("", "mirror lock check: %v", lockErr)
	} else if locked == 0 {
		if err := maybeScheduleMirror(ctx, d.c, client, enqueuer, cfg); err != nil {
			d.c.GetLogger().Error("", "mirror scheduling: %v", err)
		}
	}
}

func (d *daemon) ensureTagReconcile(ctx context.Context, cfg *config.Config, client *database.Client, enqueuer *task.Enqueuer) {
	if cfg.Enricher.TagReconcile.Enabled {
		if err := maybeScheduleAnchored(ctx, d.c, client, enqueuer, cfg, &d.tagReconcile, tagReconcileSpec); err != nil {
			d.c.GetLogger().Error("", "tag reconcile scheduling: %v", err)
		}
	}
}

func (d *daemon) ensureTagMeasure(ctx context.Context, cfg *config.Config, client *database.Client, enqueuer *task.Enqueuer) {
	if cfg.Enricher.TagMeasure.Enabled {
		if err := maybeScheduleAnchored(ctx, d.c, client, enqueuer, cfg, &d.tagMeasure, tagMeasureSpec); err != nil {
			d.c.GetLogger().Error("", "tag measure scheduling: %v", err)
		}
	}
}

func (d *daemon) ensureTagIdentity(ctx context.Context, cfg *config.Config, client *database.Client, enqueuer *task.Enqueuer) {
	if cfg.Enricher.TagAdjudicator.Enabled {
		if err := maybeScheduleAnchored(ctx, d.c, client, enqueuer, cfg, &d.tagIdentity, tagIdentitySpec); err != nil {
			d.c.GetLogger().Error("", "tag identity scheduling: %v", err)
		}
	}
}

func (d *daemon) ensureTagAudit(ctx context.Context, cfg *config.Config, client *database.Client, enqueuer *task.Enqueuer) {
	if cfg.Enricher.TagAudit.Enabled {
		if err := maybeScheduleAnchored(ctx, d.c, client, enqueuer, cfg, &d.tagAudit, tagAuditSpec); err != nil {
			d.c.GetLogger().Error("", "tag audit scheduling: %v", err)
		}
	}
}

func (d *daemon) runPollingLoop(ctx context.Context) {
	c := d.c
	var missingTools []config.ExternalTool
	var lastCfg *config.Config

	for {
		cfgPtr := c.GetConfig()
		if cfgPtr != lastCfg {
			missingTools = config.MissingExternalToolErrors(cfgPtr)
			lastCfg = cfgPtr
		}

		pollingCfg := cfgPtr.Consumer.Polling
		interval := max(time.Duration(pollingCfg.Interval)*time.Minute, time.Minute)

		client := d.client.Load()
		if pollingCfg.Enabled && config.IsWithinActiveWindows(pollingCfg.Windows) {
			locked, lockErr := client.Queries.IsLockHeld(ctx, string(types.LockMaintenance))
			if lockErr != nil {
				c.GetLogger().Error("", "polling: backup lock check: %v", lockErr)
			} else if locked > 0 {
				c.GetLogger().Debug("", "polling: skipped — backup in progress")
			} else {
				pollingTick(ctx, c, client, task.NewEnqueuer(client, d.catalog), missingTools)
			}
		} else if pollingCfg.Enabled {
			c.GetLogger().Debug("", "polling: disabled — outside active windows")
		} else {
			c.GetLogger().Debug("", "polling: disabled")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func pollingTick(ctx context.Context, c *container.Container, client *database.Client, enqueuer *task.Enqueuer, missingTools []config.ExternalTool) {
	if len(missingTools) > 0 {
		names := make([]string, len(missingTools))
		for i, t := range missingTools {
			names[i] = t.Engine
		}
		c.GetLogger().Error("", "polling: skipped — missing external tools: %s", strings.Join(names, ", "))
		return
	}

	batchID, enqueued, err := consumption.ScanAndEnqueue(ctx, c.GetConfig(), client, enqueuer, c.GetLogger())
	if err != nil {
		c.GetLogger().Error("", "polling: scan and enqueue: %v", err)
		return
	}
	if batchID == "" {
		c.GetLogger().Debug("", "polling: no new files")
		return
	}
	if enqueued == 0 {
		c.GetLogger().Info("", "polling: all files were duplicates (batch %s)", batchID)
		return
	}

	c.GetLogger().Info("", "polling: batch %s created with %d files", batchID, enqueued)
}

// The interim schedule names scheduled batches carry until schedules become declarative data on the
// type definitions.
const (
	scheduleBackup            = "backup"
	scheduleMirror            = "mirror"
	scheduleThumbnailBackfill = "thumbnail-backfill"
	scheduleTagReconcile      = "tagreconcile"
	scheduleTagMeasure        = "tagmeasure"
	scheduleTagIdentity       = "tagidentity"
	scheduleTagAudit          = "tagaudit"
)

// enqueueScheduledBatch enqueues the producer-built task set of one scheduled run, returning the full
// result. The schedule name and origin are set here, at the scheduler boundary.
func enqueueScheduledBatch(ctx context.Context, enqueuer *task.Enqueuer, scheduleName string, tasks []task.TaskSpec) (task.EnqueueResult, error) {
	spec, err := task.NewBatchSpec(types.Batch.Origin.Schedule, scheduleName)
	if err != nil {
		return task.EnqueueResult{}, fmt.Errorf("build %s batch spec: %w", scheduleName, err)
	}
	return enqueuer.EnqueueBatch(ctx, spec, tasks)
}

func maybeScheduleTask(ctx context.Context, c *container.Container, enqueuer *task.Enqueuer, scheduleName string, active func(context.Context) (int64, error), due func(context.Context) (bool, error), specs func() ([]task.TaskSpec, error)) error {
	activeCount, err := active(ctx)
	if err != nil {
		return fmt.Errorf("check active %s tasks: %w", scheduleName, err)
	}
	if activeCount > 0 {
		return nil
	}

	ok, err := due(ctx)
	if err != nil {
		return fmt.Errorf("check %s due: %w", scheduleName, err)
	}
	if !ok {
		return nil
	}

	tasks, err := specs()
	if err != nil {
		return fmt.Errorf("build %s tasks: %w", scheduleName, err)
	}
	result, err := enqueueScheduledBatch(ctx, enqueuer, scheduleName, tasks)
	if err != nil {
		return err
	}
	if result.BatchID == "" {
		return nil
	}

	c.GetLogger().Info("", "%s task %s scheduled (batch %s)", scheduleName, result.TaskIDs, result.BatchID)
	return nil
}

func maybeScheduleBackup(ctx context.Context, c *container.Container, client *database.Client, enqueuer *task.Enqueuer, cfg *config.Config) error {
	if !cfg.Backup.Enabled {
		return nil
	}

	for _, schedule := range cfg.Backup.Schedules {
		s := schedule
		if err := maybeScheduleTask(ctx, c, enqueuer, scheduleBackup,
			func(ctx context.Context) (int64, error) { return client.Queries.CountActiveBackupTasks(ctx) },
			func(ctx context.Context) (bool, error) { return backup.IsBackupDue(ctx, client.Queries, s) },
			func() ([]task.TaskSpec, error) {
				return backup.BackupSpecs(s.Mode, s.Path, s.Keep,
					backup.ScheduleTiming{Interval: s.Interval, Time: s.Time})
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func maybeScheduleMirror(ctx context.Context, c *container.Container, client *database.Client, enqueuer *task.Enqueuer, cfg *config.Config) error {
	if !cfg.Mirror.Enabled {
		return nil
	}

	return maybeScheduleTask(ctx, c, enqueuer, scheduleMirror,
		func(ctx context.Context) (int64, error) { return client.Queries.CountActiveMirrorTasks(ctx) },
		func(ctx context.Context) (bool, error) { return backup.IsMirrorDue(ctx, client.Queries, cfg.Mirror) },
		func() ([]task.TaskSpec, error) {
			return backup.MirrorSpecs(cfg.Mirror.Path, cfg.Mirror.Interval,
				backup.ScheduleTiming{Interval: cfg.Mirror.Interval, Time: cfg.Mirror.Time})
		},
	)
}

// scheduleState tracks the in-memory anchor and the timing the anchor ran
// under, so an edited schedule re-phases the next run until it succeeds and a
// reverted edit falls back to the steady rule.
type scheduleState struct {
	lastRun        time.Time
	runTiming      *backup.ScheduleTiming
	pendingRephase bool
}

func (s *scheduleState) nextDue(timing backup.ScheduleTiming) time.Time {
	if s.runTiming == nil {
		s.runTiming = &timing
	}
	if s.runTiming.Equal(timing) {
		s.pendingRephase = false
		return backup.NextBackupTime(s.lastRun, timing.Interval, timing.Time)
	}
	s.pendingRephase = true
	return backup.NextScheduledTime(s.lastRun, timing.Interval, timing.Time)
}

// markRun advances the anchor after a successful run under timing.
func (s *scheduleState) markRun(timing backup.ScheduleTiming) {
	s.lastRun = time.Now()
	s.runTiming = &timing
	s.pendingRephase = false
}

type anchoredTaskSpec struct {
	name      string
	enabled   func(*config.Config) bool
	lastBatch func(context.Context, *database.Queries) (sql.NullTime, error)
	timing    func(*config.Config) backup.ScheduleTiming
	active    func(context.Context, *database.Queries) (int64, error)
	lockGated bool
	execute   func(ctx context.Context, c *container.Container, client *database.Client, enqueuer *task.Enqueuer, state *scheduleState, timing backup.ScheduleTiming, next time.Time) error
}

var (
	thumbnailBackfillSpec = anchoredTaskSpec{
		name: "thumbnail backfill",
		enabled: func(cfg *config.Config) bool {
			return cfg.Consumer.Thumbnail.Enabled && cfg.Consumer.Thumbnail.BackfillInterval > 0
		},
		lastBatch: func(ctx context.Context, q *database.Queries) (sql.NullTime, error) {
			return q.GetLastThumbnailBackfillBatchCreatedAt(ctx)
		},
		timing: func(cfg *config.Config) backup.ScheduleTiming {
			return backup.ScheduleTiming{Interval: cfg.Consumer.Thumbnail.BackfillInterval, Time: cfg.Consumer.Thumbnail.BackfillTime}
		},
		active: func(ctx context.Context, q *database.Queries) (int64, error) {
			return q.CountActiveThumbnailBackfillBatches(ctx)
		},
		execute: executeThumbnailBackfill,
	}
	tagReconcileSpec = anchoredTaskSpec{
		name:    "tag reconcile",
		enabled: func(cfg *config.Config) bool { return cfg.Enricher.TagReconcile.Enabled },
		lastBatch: func(ctx context.Context, q *database.Queries) (sql.NullTime, error) {
			return q.GetLastTagReconcileBatchCreatedAt(ctx)
		},
		timing: func(cfg *config.Config) backup.ScheduleTiming {
			return backup.ScheduleTiming{Interval: cfg.Enricher.TagReconcile.Interval, Time: cfg.Enricher.TagReconcile.Time}
		},
		active: func(ctx context.Context, q *database.Queries) (int64, error) {
			return q.CountActiveTagReconcileBatches(ctx)
		},
		lockGated: true,
		execute:   executeTagReconcile,
	}
	tagMeasureSpec = anchoredTaskSpec{
		name:    "tag measure",
		enabled: func(cfg *config.Config) bool { return cfg.Enricher.TagMeasure.Enabled },
		lastBatch: func(ctx context.Context, q *database.Queries) (sql.NullTime, error) {
			return q.GetLastTagMeasureBatchCreatedAt(ctx)
		},
		timing: func(cfg *config.Config) backup.ScheduleTiming {
			return backup.ScheduleTiming{Interval: cfg.Enricher.TagMeasure.Interval, Time: cfg.Enricher.TagMeasure.Time}
		},
		active: func(ctx context.Context, q *database.Queries) (int64, error) {
			return q.CountActiveTagMeasureBatches(ctx)
		},
		lockGated: true,
		execute:   executeTagMeasure,
	}
	tagAuditSpec = anchoredTaskSpec{
		name:    "tag audit",
		enabled: func(cfg *config.Config) bool { return cfg.Enricher.TagAudit.Enabled },
		lastBatch: func(ctx context.Context, q *database.Queries) (sql.NullTime, error) {
			return q.GetLastTagAuditBatchCreatedAt(ctx)
		},
		timing: func(cfg *config.Config) backup.ScheduleTiming {
			return backup.ScheduleTiming{Interval: cfg.Enricher.TagAudit.Interval, Time: cfg.Enricher.TagAudit.Time}
		},
		active: func(ctx context.Context, q *database.Queries) (int64, error) {
			return q.CountActiveTagAuditBatches(ctx)
		},
		lockGated: true,
		execute:   executeTagAudit,
	}
	// identity's cadence is pinned, not configured: a fixed daily interval at an off-peak hour, gated on the
	// tag adjudicator so the schedule only fires where a judge can act
	tagIdentitySpec = anchoredTaskSpec{
		name:    "tag identity",
		enabled: func(cfg *config.Config) bool { return cfg.Enricher.TagAdjudicator.Enabled },
		lastBatch: func(ctx context.Context, q *database.Queries) (sql.NullTime, error) {
			return q.GetLastTagIdentityBatchCreatedAt(ctx)
		},
		timing: func(*config.Config) backup.ScheduleTiming {
			return backup.ScheduleTiming{Interval: 1, Time: "03:30"}
		},
		active: func(ctx context.Context, q *database.Queries) (int64, error) {
			return q.CountActiveTagIdentityBatches(ctx)
		},
		lockGated: true,
		execute:   executeTagIdentity,
	}
)

func maybeScheduleAnchored(ctx context.Context, c *container.Container, client *database.Client, enqueuer *task.Enqueuer, cfg *config.Config, state *scheduleState, spec anchoredTaskSpec) error {
	if !spec.enabled(cfg) {
		return nil
	}

	if state.lastRun.IsZero() {
		lastBatch, err := spec.lastBatch(ctx, client.Queries)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			c.GetLogger().Debug("", "%s: last batch lookup failed: %v", spec.name, err)
			return fmt.Errorf("get last %s batch: %w", spec.name, err)
		}
		if err == nil {
			state.lastRun = lastBatch.Time
		}
	}

	timing := spec.timing(cfg)
	// The virtual anchor makes the first run land at the next occurrence of the
	// preferred time; the interval counts from that first run onward.
	if state.lastRun.IsZero() {
		state.lastRun = backup.FirstRunAnchor(time.Now(), timing.Interval, timing.Time)
	}

	next := state.nextDue(timing)
	if now := time.Now(); now.Before(next) {
		return nil
	}

	active, err := spec.active(ctx, client.Queries)
	if err != nil {
		return fmt.Errorf("check active %s batches: %w", spec.name, err)
	}
	if active > 0 {
		c.GetLogger().Debug("", "%s: skipped — %d active batch(es)", spec.name, active)
		return nil
	}

	if spec.lockGated {
		locked, err := client.Queries.IsLockHeld(ctx, string(types.LockMaintenance))
		if err != nil {
			return fmt.Errorf("backup lock check: %w", err)
		}
		if locked > 0 {
			c.GetLogger().Debug("", "%s: skipped — maintenance lock held", spec.name)
			return nil
		}
	}

	return spec.execute(ctx, c, client, enqueuer, state, timing, next)
}

func executeThumbnailBackfill(ctx context.Context, c *container.Container, client *database.Client, enqueuer *task.Enqueuer, state *scheduleState, timing backup.ScheduleTiming, next time.Time) error {
	specs, err := service.ThumbnailBackfillSpecs(ctx, client.Queries)
	if err != nil {
		c.GetLogger().Error("", "enqueue thumbnail backfill: %v (last_run=%s, next=%s)", err, state.lastRun.Format(time.RFC3339), next.Format(time.RFC3339))
		return nil
	}
	result, err := enqueueScheduledBatch(ctx, enqueuer, scheduleThumbnailBackfill, specs)
	if err != nil {
		c.GetLogger().Error("", "enqueue thumbnail backfill: %v (last_run=%s, next=%s)", err, state.lastRun.Format(time.RFC3339), next.Format(time.RFC3339))
		return nil
	}

	// advance only after a successful enqueue so a failed run retries next tick
	state.markRun(timing)
	if result.BatchID == "" {
		c.GetLogger().Debug("", "thumbnail backfill: no documents missing thumbnails")
		return nil
	}
	c.GetLogger().Info("", "thumbnail backfill batch %s scheduled (%d tasks)", result.BatchID, len(result.TaskIDs))
	c.GetLogger().Debug("", "thumbnail backfill: enqueued (batch=%s, last_run=%s)", result.BatchID, state.lastRun.Format(time.RFC3339))
	return nil
}

func executeTagReconcile(ctx context.Context, c *container.Container, _ *database.Client, enqueuer *task.Enqueuer, state *scheduleState, timing backup.ScheduleTiming, _ time.Time) error {
	return enqueueScheduled(ctx, c, enqueuer, state, timing, curation.ReconcileSpecs(), scheduleTagReconcile, "tag reconcile")
}

func executeTagMeasure(ctx context.Context, c *container.Container, _ *database.Client, enqueuer *task.Enqueuer, state *scheduleState, timing backup.ScheduleTiming, _ time.Time) error {
	return enqueueScheduled(ctx, c, enqueuer, state, timing, curation.MeasureSpecs(), scheduleTagMeasure, "tag measure")
}

func executeTagAudit(ctx context.Context, c *container.Container, _ *database.Client, enqueuer *task.Enqueuer, state *scheduleState, timing backup.ScheduleTiming, _ time.Time) error {
	return enqueueScheduled(ctx, c, enqueuer, state, timing, curation.AuditSpecs(), scheduleTagAudit, "tag audit")
}

func executeTagIdentity(ctx context.Context, c *container.Container, _ *database.Client, enqueuer *task.Enqueuer, state *scheduleState, timing backup.ScheduleTiming, _ time.Time) error {
	return enqueueScheduled(ctx, c, enqueuer, state, timing, curation.IdentitySpecs(), scheduleTagIdentity, "tag identity")
}

func enqueueScheduled(ctx context.Context, c *container.Container, enqueuer *task.Enqueuer, state *scheduleState, timing backup.ScheduleTiming, tasks []task.TaskSpec, scheduleName, name string) error {
	result, err := enqueueScheduledBatch(ctx, enqueuer, scheduleName, tasks)
	if err != nil {
		return err
	}
	// advance only after a successful enqueue so a failed schedule retries next tick
	state.markRun(timing)
	if result.BatchID != "" {
		c.GetLogger().Info("", "%s scheduled (batch %s)", name, result.BatchID)
	}
	return nil
}

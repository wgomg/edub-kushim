// Package queue is the queue daemon: it LISTENs for batch and task notifications, polls as a safety
// net, claims queued consume batches for the per-batch consume children, dispatches every other task
// type to their handlers, runs the blocking-task sweeps (backup, mirror, tag measure, tag audit, tag
// reconcile, tag identity, thumbnails backfill) under the maintenance lock, and requeues stale work.
// The per-batch consume work itself forks kushim consume children.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
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

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/consume"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

type daemon struct {
	c        *container.Container
	client   atomic.Pointer[database.Client]
	batchSvc *service.Batch

	notifyCancel context.CancelFunc
	notifyCh     chan struct{}

	backupPool       poolState
	mirrorPool       poolState
	tagReconcilePool poolState
	tagMeasurePool   poolState
	tagIdentityPool  poolState
	tagAuditPool     poolState

	thumbnailBackfill scheduleState
	tagReconcile      scheduleState
	tagMeasure        scheduleState
	tagIdentity       scheduleState
	tagAudit          scheduleState

	lastStaleTaskClaim time.Time
}

type poolState struct {
	pool    *task.Pool
	started bool
}

// Run is the kushim queue verb: it runs the daemon loop until signalled,
// claiming batches and tasks and forking the blocking-task children.
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

	maxConcurrent := c.GetConfig().Srv.MaxConcurrentBatches
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}

	c.GetLogger().Info("", "queue daemon started (max %d concurrent batches)", maxConcurrent)

	d := &daemon{
		c:        c,
		batchSvc: service.NewBatch(client, c.GetConfig().Consumer.Reclaim.MaxRetries),
	}
	d.client.Store(client)

	go d.runPollingLoop(ctx, maxConcurrent)

	d.startNotify(ctx)
	defer d.stopNotify()

	safetyInterval := 30 * time.Second
	safetyTimer := time.NewTimer(safetyInterval)
	defer safetyTimer.Stop()

	hkTicker := time.NewTicker(5 * time.Second)
	defer hkTicker.Stop()

	for {
		select {
		case <-d.notifyCh:
			if err := consumeNextQueuedBatch(ctx, d.client.Load(), d.batchSvc, maxConcurrent, c.GetLogger()); err != nil {
				c.GetLogger().Error("", "queue consumption: %v", err)
			}
			if !safetyTimer.Stop() {
				select {
				case <-safetyTimer.C:
				default:
				}
			}
			safetyTimer.Reset(safetyInterval)

		case <-safetyTimer.C:
			if err := consumeNextQueuedBatch(ctx, d.client.Load(), d.batchSvc, maxConcurrent, c.GetLogger()); err != nil {
				c.GetLogger().Error("", "queue consumption: %v", err)
			}
			safetyTimer.Reset(safetyInterval)

		case <-hkTicker.C:
			d.tick(ctx)

		case <-ctx.Done():
			c.GetLogger().Info("", "queue daemon stopped")
			d.stopPools()
			return nil
		}
	}
}

func (d *daemon) startNotify(ctx context.Context) {
	notifyCtx, cancel := context.WithCancel(ctx)
	d.notifyCancel = cancel
	d.notifyCh = make(chan struct{}, 4)
	go listenForBatchNotifications(notifyCtx, config.BuildPostgresDSN(d.c.GetConfig().Db), d.notifyCh, d.c.GetLogger())
}

func (d *daemon) stopNotify() {
	if d.notifyCancel != nil {
		d.notifyCancel()
	}
}

func startPool(ctx context.Context, c *container.Container, taskType types.TaskType, state *poolState) error {
	if state.started {
		return nil
	}
	pool, err := c.GetPool(taskType)
	if err != nil {
		return err
	}
	pool.Start(ctx)
	state.pool = pool
	state.started = true
	c.GetLogger().Info("", "%s pool started (delayed)", taskType)
	return nil
}

func stopPool(state *poolState) {
	if !state.started || state.pool == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state.pool.Stop(stopCtx)
	state.pool = nil
	state.started = false
}

func (d *daemon) stopPools() {
	stopPool(&d.backupPool)
	stopPool(&d.mirrorPool)
	stopPool(&d.tagReconcilePool)
	stopPool(&d.tagMeasurePool)
	stopPool(&d.tagIdentityPool)
	stopPool(&d.tagAuditPool)
}

func (d *daemon) tick(ctx context.Context) {
	d.reloadConfig(ctx)
	cfg := d.c.GetConfig()

	d.ensureBackup(ctx, cfg)
	d.ensureMirror(ctx, cfg)

	if cfg.Consumer.Thumbnail.Enabled && cfg.Consumer.Thumbnail.BackfillInterval > 0 {
		if err := maybeScheduleAnchored(ctx, d.c, d.client.Load(), cfg, &d.thumbnailBackfill, thumbnailBackfillSpec); err != nil {
			d.c.GetLogger().Error("", "thumbnail backfill scheduling: %v", err)
		}
	}

	d.ensureTagReconcile(ctx, cfg)
	d.ensureTagMeasure(ctx, cfg)
	d.ensureTagIdentity(ctx, cfg)
	d.ensureTagAudit(ctx, cfg)

	if err := sweepEmptyQueuedBatches(ctx, d.client.Load(), d.c.GetLogger()); err != nil {
		d.c.GetLogger().Error("", "empty queued batch sweep: %v", err)
	}

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

	d.stopPools()
	d.client.Store(newClient)
	d.batchSvc = service.NewBatch(newClient, d.c.GetConfig().Consumer.Reclaim.MaxRetries)

	d.stopNotify()
	d.startNotify(ctx)
}

func (d *daemon) ensureBackup(ctx context.Context, cfg *config.Config) {
	if cfg.Backup.Enabled {
		if err := startPool(ctx, d.c, types.Task.Type.Backup, &d.backupPool); err != nil {
			d.c.GetLogger().Error("", "backup pool: %v", err)
		}
		locked, lockErr := d.client.Load().Queries.IsMaintenanceLocked(ctx)
		if lockErr != nil {
			d.c.GetLogger().Error("", "backup lock check: %v", lockErr)
		} else if locked == 0 {
			if err := maybeScheduleBackup(ctx, d.c, d.client.Load(), cfg); err != nil {
				d.c.GetLogger().Error("", "backup scheduling: %v", err)
			}
		}
	} else if d.backupPool.started {
		stopPool(&d.backupPool)
		d.c.GetLogger().Info("", "backup pool stopped (backup disabled)")
	}
}

func (d *daemon) ensureMirror(ctx context.Context, cfg *config.Config) {
	if cfg.Mirror.Enabled {
		if err := startPool(ctx, d.c, types.Task.Type.Mirror, &d.mirrorPool); err != nil {
			d.c.GetLogger().Error("", "mirror pool: %v", err)
		}
		locked, lockErr := d.client.Load().Queries.IsMaintenanceLocked(ctx)
		if lockErr != nil {
			d.c.GetLogger().Error("", "mirror lock check: %v", lockErr)
		} else if locked == 0 {
			if err := maybeScheduleMirror(ctx, d.c, d.client.Load(), cfg); err != nil {
				d.c.GetLogger().Error("", "mirror scheduling: %v", err)
			}
		}
	} else if d.mirrorPool.started {
		stopPool(&d.mirrorPool)
		d.c.GetLogger().Info("", "mirror pool stopped (mirror disabled)")
	}
}

func (d *daemon) ensureTagReconcile(ctx context.Context, cfg *config.Config) {
	if err := startPool(ctx, d.c, types.Task.Type.TagReconcile, &d.tagReconcilePool); err != nil {
		d.c.GetLogger().Error("", "tag reconcile pool: %v", err)
	}
	if cfg.Enricher.TagReconcile.Enabled {
		if err := maybeScheduleAnchored(ctx, d.c, d.client.Load(), cfg, &d.tagReconcile, tagReconcileSpec); err != nil {
			d.c.GetLogger().Error("", "tag reconcile scheduling: %v", err)
		}
	}
}

func (d *daemon) ensureTagMeasure(ctx context.Context, cfg *config.Config) {
	if err := startPool(ctx, d.c, types.Task.Type.TagMeasure, &d.tagMeasurePool); err != nil {
		d.c.GetLogger().Error("", "tag measure pool: %v", err)
	}
	if cfg.Enricher.TagMeasure.Enabled {
		if err := maybeScheduleAnchored(ctx, d.c, d.client.Load(), cfg, &d.tagMeasure, tagMeasureSpec); err != nil {
			d.c.GetLogger().Error("", "tag measure scheduling: %v", err)
		}
	}
}

func (d *daemon) ensureTagIdentity(ctx context.Context, cfg *config.Config) {
	if err := startPool(ctx, d.c, types.Task.Type.TagIdentity, &d.tagIdentityPool); err != nil {
		d.c.GetLogger().Error("", "tag identity pool: %v", err)
	}
	if cfg.Enricher.TagAdjudicator.Enabled {
		if err := maybeScheduleAnchored(ctx, d.c, d.client.Load(), cfg, &d.tagIdentity, tagIdentitySpec); err != nil {
			d.c.GetLogger().Error("", "tag identity scheduling: %v", err)
		}
	}
}

func (d *daemon) ensureTagAudit(ctx context.Context, cfg *config.Config) {
	if err := startPool(ctx, d.c, types.Task.Type.TagAudit, &d.tagAuditPool); err != nil {
		d.c.GetLogger().Error("", "tag audit pool: %v", err)
	}
	if cfg.Enricher.TagAudit.Enabled {
		if err := maybeScheduleAnchored(ctx, d.c, d.client.Load(), cfg, &d.tagAudit, tagAuditSpec); err != nil {
			d.c.GetLogger().Error("", "tag audit scheduling: %v", err)
		}
	}
}

func (d *daemon) reclaimStale(ctx context.Context, cfg *config.Config) {
	if !cfg.Consumer.Reclaim.Enabled {
		return
	}
	if err := reclaimStaleBatches(ctx, cfg, d.client.Load(), d.batchSvc, d.c.GetLogger()); err != nil {
		d.c.GetLogger().Error("", "stale reclamation: %v", err)
	}
	minInterval := max(cfg.Consumer.Reclaim.StaleTaskAfter/10, 60)
	if time.Since(d.lastStaleTaskClaim) > time.Duration(minInterval)*time.Second {
		if err := reclaimStaleTasks(ctx, d.batchSvc, cfg, d.c.GetLogger()); err != nil {
			d.c.GetLogger().Error("", "stale task reclamation: %v", err)
		}
		d.lastStaleTaskClaim = time.Now()
	}
}

// Zero-task queued batches are never finalized and would block the polling gate.
func sweepEmptyQueuedBatches(ctx context.Context, client *database.Client, logger *log.Logger) error {
	ids, err := client.Queries.ListEmptyQueuedBatches(ctx, 10)
	if err != nil {
		return fmt.Errorf("list empty queued batches: %w", err)
	}
	for _, id := range ids {
		if err := client.Queries.SetBatchFailed(ctx, id); err != nil {
			logger.Error("", "fail empty queued batch %s: %v", id, err)
			continue
		}
		logger.Info("", "reaped empty queued batch %s (no tasks after 10 minutes)", id)
	}
	return nil
}

func reclaimStaleBatches(ctx context.Context, cfg *config.Config, client *database.Client, batchSvc *service.Batch, logger *log.Logger) error {
	owners, err := batchSvc.ListStaleBatchOwners(ctx)
	if err != nil {
		return fmt.Errorf("list stale batch owners: %w", err)
	}
	for _, owner := range owners {
		logger.Info("", "reclaiming stale batch %s (owner=%s pid=%d)", owner.BatchID, owner.OwnerID, owner.Pid)
		if consume.IsAlive(owner.Pid) {
			if err := syscall.Kill(int(owner.Pid), syscall.SIGTERM); err != nil {
				logger.Error("", "signal SIGTERM to PID %d for batch %s: %v", owner.Pid, owner.BatchID, err)
			}
		}
		if _, err := batchSvc.ResetProcessingTasksByBatch(ctx, owner.BatchID); err != nil {
			logger.Error("", "reset processing tasks for batch %s: %v", owner.BatchID, err)
			continue
		}
		if err := consumption.QuarantineFailedFiles(ctx, client.Queries, cfg.Storage.StorageDir, logger, owner.BatchID); err != nil {
			logger.Error("", "quarantine files for batch %s: %v", owner.BatchID, err)
		}

		hasWork, err := batchSvc.HasPendingWork(ctx, owner.BatchID)
		if err != nil {
			logger.Error("", "check pending work for batch %s: %v", owner.BatchID, err)
			continue
		}
		if !hasWork {
			if err := batchSvc.SetBatchFailed(ctx, owner.BatchID); err != nil {
				logger.Error("", "set batch failed %s: %v", owner.BatchID, err)
			}
		} else {
			if err := batchSvc.RequeueBatch(ctx, owner.BatchID); err != nil {
				logger.Error("", "requeue batch %s: %v", owner.BatchID, err)
				continue
			}
		}
		if err := batchSvc.DeleteBatchOwnerByBatchID(ctx, owner.BatchID); err != nil {
			logger.Error("", "delete batch owner for batch %s: %v", owner.BatchID, err)
			continue
		}
		if hasWork {
			logger.Info("", "stale batch %s reclaimed and requeued", owner.BatchID)
		} else {
			logger.Info("", "stale batch %s failed — all tasks quarantined", owner.BatchID)
		}
	}
	return nil
}

func reclaimStaleTasks(ctx context.Context, batchSvc *service.Batch, cfg *config.Config, logger *log.Logger) error {
	count, err := batchSvc.ResetStaleProcessingTasks(ctx, cfg.Consumer.Reclaim.StaleTaskAfter)
	if err != nil {
		return fmt.Errorf("reset stale processing tasks: %w", err)
	}
	if count > 0 {
		logger.Info("", "reclaimed %d stale processing tasks (age > %ds)", count, cfg.Consumer.Reclaim.StaleTaskAfter)
	}
	return nil
}

func consumeNextQueuedBatch(ctx context.Context, client *database.Client, batchSvc *service.Batch, maxConcurrent int, logger *log.Logger) error {
	liveCount, err := batchSvc.CountLiveBatches(ctx)
	if err != nil {
		return fmt.Errorf("count live batches: %w", err)
	}
	if liveCount >= int64(maxConcurrent) {
		return nil
	}

	batch, err := batchSvc.GetNextQueuedBatch(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("get next queued batch: %w", err)
	}

	logger.Info("", "consuming queued batch %s", batch.ID)

	if err := batchSvc.SetBatchProcessing(ctx, batch.ID); err != nil {
		return fmt.Errorf("set batch %s processing: %w", batch.ID, err)
	}

	// Pre-create batch_owner row so stale reclamation can catch orphaned
	// batches if the child crashes before acquiring the owner itself.
	ownerID := "queue-" + batch.ID[:8]
	if _, err := client.AcquireBatchOwnerForce(ctx, database.AcquireBatchOwnerForceParams{
		BatchID: batch.ID,
		OwnerID: ownerID,
		Pid:     int64(os.Getpid()),
	}); err != nil {
		logger.Error("", "pre-create owner for batch %s: %v", batch.ID, err)
	}

	// --force so the child overwrites our placeholder owner row.
	cmd := exec.Command(os.Args[0], "consume", "--batch", batch.ID, "--force")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		logger.Error("", "fork consume for batch %s: %v — requeueing", batch.ID, err)
		if requeueErr := batchSvc.RequeueBatch(ctx, batch.ID); requeueErr != nil {
			logger.Error("", "requeue batch %s after fork failure: %v", batch.ID, requeueErr)
		}
		return nil
	}
	go func() { _ = cmd.Wait() }()

	logger.Info("", "forked kushim consume --batch %s (PID %d)", batch.ID, cmd.Process.Pid)
	return nil
}

func (d *daemon) runPollingLoop(ctx context.Context, maxConcurrent int) {
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
		batchSvc := service.NewBatch(client, cfgPtr.Consumer.Reclaim.MaxRetries)
		if pollingCfg.Enabled && config.IsWithinActiveWindows(pollingCfg.Windows) {
			locked, lockErr := client.Queries.IsMaintenanceLocked(ctx)
			if lockErr != nil {
				c.GetLogger().Error("", "polling: backup lock check: %v", lockErr)
			} else if locked > 0 {
				c.GetLogger().Debug("", "polling: skipped — backup in progress")
			} else {
				pollingTick(ctx, c, client, batchSvc, maxConcurrent, missingTools)
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

func pollingTick(ctx context.Context, c *container.Container, client *database.Client, batchSvc *service.Batch, maxConcurrent int, missingTools []config.ExternalTool) {
	// Only count batches that occupy a consume slot; config/backup/mirror
	// batches own their lifecycle in their handlers (see GetNextQueuedBatch).
	queuedCount, err := batchSvc.CountQueuedConsumeBatches(ctx)
	if err != nil {
		c.GetLogger().Error("", "polling: count queued batches: %v", err)
		return
	}
	liveCount, err := batchSvc.CountLiveBatches(ctx)
	if err != nil {
		c.GetLogger().Error("", "polling: count live batches: %v", err)
		return
	}
	if queuedCount+liveCount >= int64(maxConcurrent) {
		c.GetLogger().Debug("", "polling: skipped — %d live, %d queued (max %d)", liveCount, queuedCount, maxConcurrent)
		return
	}

	if len(missingTools) > 0 {
		names := make([]string, len(missingTools))
		for i, t := range missingTools {
			names[i] = t.Engine
		}
		c.GetLogger().Error("", "polling: skipped — missing external tools: %s", strings.Join(names, ", "))
		return
	}

	batchID, enqueued, err := consumption.ScanAndEnqueue(ctx, c.GetConfig(), client, c.GetLogger())
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

func enqueueOwnBatch(ctx context.Context, c *container.Container, client *database.Client, taskType types.TaskType, source types.BatchSource, payload json.RawMessage) (batchID, taskID string, err error) {
	dispatcher, err := c.GetDispatcher()
	if err != nil {
		return "", "", fmt.Errorf("get dispatcher: %w", err)
	}

	batchID = uuid.New().String()
	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Source: source,
		Status: types.Batch.Status.Queued,
	}); err != nil {
		return "", "", fmt.Errorf("create %s batch: %w", taskType, err)
	}

	taskID = uuid.New().String()
	if _, err := dispatcher.Enqueue(ctx, taskType, batchID, payload, taskID); err != nil {
		return "", "", fmt.Errorf("enqueue %s task: %w", taskType, err)
	}
	return batchID, taskID, nil
}

func maybeScheduleTask(ctx context.Context, c *container.Container, client *database.Client, taskType types.TaskType, source types.BatchSource, active func(context.Context) (int64, error), due func(context.Context) (bool, error), payload map[string]any) error {
	activeCount, err := active(ctx)
	if err != nil {
		return fmt.Errorf("check active %s tasks: %w", taskType, err)
	}
	if activeCount > 0 {
		return nil
	}

	ok, err := due(ctx)
	if err != nil {
		return fmt.Errorf("check %s due: %w", taskType, err)
	}
	if !ok {
		return nil
	}

	payloadJSON, _ := json.Marshal(payload)
	batchID, taskID, err := enqueueOwnBatch(ctx, c, client, taskType, source, payloadJSON)
	if err != nil {
		return err
	}

	c.GetLogger().Info("", "%s task %s scheduled (batch %s)", taskType, taskID, batchID)
	return nil
}

func maybeScheduleBackup(ctx context.Context, c *container.Container, client *database.Client, cfg *config.Config) error {
	if !cfg.Backup.Enabled {
		return nil
	}

	for _, schedule := range cfg.Backup.Schedules {
		s := schedule
		payload := map[string]any{
			"mode":     s.Mode,
			"path":     s.Path,
			"keep":     s.Keep,
			"schedule": backup.ScheduleTiming{Interval: s.Interval, Time: s.Time},
		}
		if err := maybeScheduleTask(ctx, c, client, types.Task.Type.Backup, types.Batch.Source.Backup,
			func(ctx context.Context) (int64, error) { return client.Queries.CountActiveBackupTasks(ctx) },
			func(ctx context.Context) (bool, error) { return backup.IsBackupDue(ctx, client.Queries, s) },
			payload,
		); err != nil {
			return err
		}
	}
	return nil
}

func maybeScheduleMirror(ctx context.Context, c *container.Container, client *database.Client, cfg *config.Config) error {
	if !cfg.Mirror.Enabled {
		return nil
	}

	payload := map[string]any{
		"path":     cfg.Mirror.Path,
		"interval": cfg.Mirror.Interval,
		"schedule": backup.ScheduleTiming{Interval: cfg.Mirror.Interval, Time: cfg.Mirror.Time},
	}
	return maybeScheduleTask(ctx, c, client, types.Task.Type.Mirror, types.Batch.Source.Mirror,
		func(ctx context.Context) (int64, error) { return client.Queries.CountActiveMirrorTasks(ctx) },
		func(ctx context.Context) (bool, error) { return backup.IsMirrorDue(ctx, client.Queries, cfg.Mirror) },
		payload,
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
	execute   func(ctx context.Context, c *container.Container, client *database.Client, state *scheduleState, timing backup.ScheduleTiming, next time.Time) error
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

func maybeScheduleAnchored(ctx context.Context, c *container.Container, client *database.Client, cfg *config.Config, state *scheduleState, spec anchoredTaskSpec) error {
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
		locked, err := client.Queries.IsMaintenanceLocked(ctx)
		if err != nil {
			return fmt.Errorf("backup lock check: %w", err)
		}
		if locked > 0 {
			c.GetLogger().Debug("", "%s: skipped — maintenance lock held", spec.name)
			return nil
		}
	}

	return spec.execute(ctx, c, client, state, timing, next)
}

func executeThumbnailBackfill(_ context.Context, c *container.Container, _ *database.Client, state *scheduleState, timing backup.ScheduleTiming, next time.Time) error {
	cmd := exec.Command(os.Args[0], "thumbnails", "--all")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		c.GetLogger().Error("", "fork kushim thumbnails --all: %v (last_run=%s, next=%s)", err, state.lastRun.Format(time.RFC3339), next.Format(time.RFC3339))
		return nil
	}
	go func() { _ = cmd.Wait() }()

	// Advance only after a successful fork so a failed start retries next tick.
	state.markRun(timing)
	c.GetLogger().Info("", "forked kushim thumbnails --all (PID %d)", cmd.Process.Pid)
	c.GetLogger().Debug("", "thumbnail backfill: forked (pid=%d, last_run=%s)", cmd.Process.Pid, state.lastRun.Format(time.RFC3339))
	return nil
}

func executeTagReconcile(ctx context.Context, c *container.Container, client *database.Client, state *scheduleState, timing backup.ScheduleTiming, _ time.Time) error {
	return enqueueScheduled(ctx, c, client, state, timing, types.Task.Type.TagReconcile, types.Batch.Source.TagReconcile, "tag reconcile")
}

func executeTagMeasure(ctx context.Context, c *container.Container, client *database.Client, state *scheduleState, timing backup.ScheduleTiming, _ time.Time) error {
	return enqueueScheduled(ctx, c, client, state, timing, types.Task.Type.TagMeasure, types.Batch.Source.TagMeasure, "tag measure")
}

func executeTagAudit(ctx context.Context, c *container.Container, client *database.Client, state *scheduleState, timing backup.ScheduleTiming, _ time.Time) error {
	return enqueueScheduled(ctx, c, client, state, timing, types.Task.Type.TagAudit, types.Batch.Source.TagAudit, "tag audit")
}

func executeTagIdentity(ctx context.Context, c *container.Container, client *database.Client, state *scheduleState, timing backup.ScheduleTiming, _ time.Time) error {
	return enqueueScheduled(ctx, c, client, state, timing, types.Task.Type.TagIdentity, types.Batch.Source.TagIdentity, "tag identity")
}

func enqueueScheduled(ctx context.Context, c *container.Container, client *database.Client, state *scheduleState, timing backup.ScheduleTiming, taskType types.TaskType, source types.BatchSource, name string) error {
	batchID, taskID, err := enqueueOwnBatch(ctx, c, client, taskType, source, json.RawMessage("{}"))
	if err != nil {
		return err
	}
	// Advance only after a successful enqueue so a failed schedule retries next tick.
	state.markRun(timing)
	c.GetLogger().Info("", "%s task %s scheduled (batch %s)", name, taskID, batchID)
	return nil
}

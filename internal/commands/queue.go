package commands

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
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func queueHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim queue [--bg]") {
		return nil
	}

	bgFlag := false
	fp.Bool("--bg", &bgFlag)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	pidFile := filepath.Join(c.cfg.Load().App.ConfigDir, "kushim-queue.pid")
	logFile := filepath.Join(c.cfg.Load().App.ConfigDir, "logs", "queue.log")

	os.MkdirAll(filepath.Dir(logFile), 0755)
	if err := c.logger.SetLogFile(utils.LogFileConfig{
		Path:       logFile,
		MaxSize:    c.cfg.Load().App.Logging.MaxSize,
		MaxBackups: c.cfg.Load().App.Logging.MaxBackups,
		MaxAge:     c.cfg.Load().App.Logging.MaxAge,
		Compress:   c.cfg.Load().App.Logging.Compress,
	}); err != nil {
		c.logger.Error(nil, "failed to open queue log file: %v", err)
	}

	if bgFlag {
		cmd := exec.Command(os.Args[0], "queue")
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start background queue daemon: %w", err)
		}
		c.logger.Info(nil, "queue daemon started in background (PID %d)", cmd.Process.Pid)
		return nil
	}

	if data, err := os.ReadFile(pidFile); err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && syscall.Kill(pid, 0) == nil {
			return fmt.Errorf("queue daemon already running (PID %d)", pid)
		}
		c.logger.Info(nil, "removing stale PID file from process %d", pid)
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
			c.logger.Info(nil, "shutting down queue daemon...")
			os.Remove(pidFile)
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	batchSvc := service.NewBatch(client, c.cfg.Load().Consumer.Reclaim.MaxRetries)
	maxConcurrent := c.cfg.Load().Srv.MaxConcurrentBatches
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}

	c.logger.Info(nil, "queue daemon started (max %d concurrent batches)", maxConcurrent)

	var backupPool *pool.Pool
	var backupPoolStarted bool
	var mirrorPool *pool.Pool
	var mirrorPoolStarted bool

	go runPollingLoop(ctx, c, func() *database.Client { return client }, func() *service.Batch { return batchSvc }, maxConcurrent)

	var notifyCancel context.CancelFunc
	stopNotify := func() {
		if notifyCancel != nil {
			notifyCancel()
		}
	}
	defer stopNotify()

	notifyCtx, cancel := context.WithCancel(ctx)
	notifyCancel = cancel
	notifyCh := make(chan struct{}, 4)
	go listenForBatchNotifications(notifyCtx, config.BuildPostgresDSN(c.cfg.Load().Db), notifyCh, c.logger)

	safetyInterval := 30 * time.Second
	safetyTimer := time.NewTimer(safetyInterval)
	defer safetyTimer.Stop()

	hkTicker := time.NewTicker(5 * time.Second)
	defer hkTicker.Stop()

	var lastStaleTaskClaim time.Time
	var lastThumbnailBackfill time.Time

	for {
		select {
		case <-notifyCh:
			if err := consumeNextQueuedBatch(ctx, client, batchSvc, maxConcurrent, c.logger); err != nil {
				c.logger.Error(nil, "queue consumption: %v", err)
			}
			if !safetyTimer.Stop() {
				select {
				case <-safetyTimer.C:
				default:
				}
			}
			safetyTimer.Reset(safetyInterval)

		case <-safetyTimer.C:
			if err := consumeNextQueuedBatch(ctx, client, batchSvc, maxConcurrent, c.logger); err != nil {
				c.logger.Error(nil, "queue consumption: %v", err)
			}
			safetyTimer.Reset(safetyInterval)

		case <-hkTicker.C:
			if newCfg, err := config.Load(c.cfg.Load().App.ConfigDir); err != nil {
				c.logger.Error(nil, "config reload: %v", err)
			} else {
				oldCfg := c.cfg.Load()
				c.cfg.Store(newCfg)
				if config.DatabaseConnectionChanged(oldCfg.Db, newCfg.Db) {
					c.logger.Info(nil, "DB config changed, reconnecting...")

					newClient, err := c.reconnectClient()
					if err != nil {
						c.logger.Error(nil, "failed to reconnect to new DB: %v — keeping previous configuration", err)
						c.cfg.Store(oldCfg)
					} else {
						if backupPoolStarted {
							stopPool(c, "backup", &backupPoolStarted, &backupPool)
						}
						if mirrorPoolStarted {
							stopPool(c, "mirror", &mirrorPoolStarted, &mirrorPool)
						}
						client = newClient
						batchSvc = service.NewBatch(client, c.cfg.Load().Consumer.Reclaim.MaxRetries)

						stopNotify()
						notifyCtx, cancel := context.WithCancel(ctx)
						notifyCancel = cancel
						notifyCh = make(chan struct{}, 4)
						go listenForBatchNotifications(notifyCtx, config.BuildPostgresDSN(c.cfg.Load().Db), notifyCh, c.logger)
					}
				}
			}
			cfg := c.cfg.Load()

			if cfg.Backup.Enabled {
				if !backupPoolStarted {
					if err := startPool(c, ctx, "backup", &backupPoolStarted, &backupPool); err != nil {
						c.logger.Error(nil, "backup pool: %v", err)
					}
				}
				locked, lockErr := client.Queries.IsBackupLocked(ctx)
				if lockErr != nil {
					c.logger.Error(nil, "backup lock check: %v", lockErr)
				} else if locked == 0 {
					if err := maybeScheduleBackup(ctx, c, client, cfg); err != nil {
						c.logger.Error(nil, "backup scheduling: %v", err)
					}
				}
			} else if backupPoolStarted {
				stopPool(c, "backup", &backupPoolStarted, &backupPool)
				c.logger.Info(nil, "backup pool stopped (backup disabled)")
			}

			if cfg.Mirror.Enabled {
				if !mirrorPoolStarted {
					if err := startPool(c, ctx, "mirror", &mirrorPoolStarted, &mirrorPool); err != nil {
						c.logger.Error(nil, "mirror pool: %v", err)
					}
				}
				locked, lockErr := client.Queries.IsBackupLocked(ctx)
				if lockErr != nil {
					c.logger.Error(nil, "mirror lock check: %v", lockErr)
				} else if locked == 0 {
					if err := maybeScheduleMirror(ctx, c, client, cfg); err != nil {
						c.logger.Error(nil, "mirror scheduling: %v", err)
					}
				}
			} else if mirrorPoolStarted {
				stopPool(c, "mirror", &mirrorPoolStarted, &mirrorPool)
				c.logger.Info(nil, "mirror pool stopped (mirror disabled)")
			}

			if cfg.Consumer.Thumbnail.Enabled && cfg.Consumer.Thumbnail.BackfillInterval > 0 {
				if err := maybeScheduleThumbnailBackfill(ctx, c, client, cfg, &lastThumbnailBackfill); err != nil {
					c.logger.Error(nil, "thumbnail backfill scheduling: %v", err)
				}
			}

			if cfg.Consumer.Reclaim.Enabled {
				if err := reclaimStaleBatches(ctx, cfg, client, batchSvc, c.logger); err != nil {
					c.logger.Error(nil, "stale reclamation: %v", err)
				}
				minInterval := max(cfg.Consumer.Reclaim.StaleTaskAfter/10, 60)
				if time.Since(lastStaleTaskClaim) > time.Duration(minInterval)*time.Second {
					if err := reclaimStaleTasks(ctx, batchSvc, cfg, c.logger); err != nil {
						c.logger.Error(nil, "stale task reclamation: %v", err)
					}
					lastStaleTaskClaim = time.Now()
				}
			}

		case <-ctx.Done():
			c.logger.Info(nil, "queue daemon stopped")
			stopPool(c, "backup", &backupPoolStarted, &backupPool)
			stopPool(c, "mirror", &mirrorPoolStarted, &mirrorPool)
			return nil
		}
	}
}

func reclaimStaleBatches(ctx context.Context, cfg *config.Config, client *database.Client, batchSvc *service.Batch, logger *utils.Logger) error {
	owners, err := batchSvc.ListStaleBatchOwners(ctx)
	if err != nil {
		return fmt.Errorf("list stale batch owners: %w", err)
	}
	for _, owner := range owners {
		logger.Info(nil, "reclaiming stale batch %s (owner=%s pid=%d)", owner.BatchID, owner.OwnerID, owner.Pid)
		if isAlive(owner.Pid) {
			if err := syscall.Kill(int(owner.Pid), syscall.SIGTERM); err != nil {
				logger.Error(nil, "signal SIGTERM to PID %d for batch %s: %v", owner.Pid, owner.BatchID, err)
			}
		}
		if _, err := batchSvc.ResetProcessingTasksByBatch(ctx, owner.BatchID); err != nil {
			logger.Error(nil, "reset processing tasks for batch %s: %v", owner.BatchID, err)
			continue
		}
		if err := consumption.QuarantineFailedFiles(ctx, client.Queries, cfg.Storage.StorageDir, logger, owner.BatchID); err != nil {
			logger.Error(nil, "quarantine files for batch %s: %v", owner.BatchID, err)
		}

		hasWork, err := batchSvc.HasPendingWork(ctx, owner.BatchID)
		if err != nil {
			logger.Error(nil, "check pending work for batch %s: %v", owner.BatchID, err)
			continue
		}
		if !hasWork {
			if err := batchSvc.SetBatchFailed(ctx, owner.BatchID); err != nil {
				logger.Error(nil, "set batch failed %s: %v", owner.BatchID, err)
			}
		} else {
			if err := batchSvc.RequeueBatch(ctx, owner.BatchID); err != nil {
				logger.Error(nil, "requeue batch %s: %v", owner.BatchID, err)
				continue
			}
		}
		if err := batchSvc.DeleteBatchOwnerByBatchID(ctx, owner.BatchID); err != nil {
			logger.Error(nil, "delete batch owner for batch %s: %v", owner.BatchID, err)
			continue
		}
		if hasWork {
			logger.Info(nil, "stale batch %s reclaimed and requeued", owner.BatchID)
		} else {
			logger.Info(nil, "stale batch %s failed — all tasks quarantined", owner.BatchID)
		}
	}
	return nil
}

func reclaimStaleTasks(ctx context.Context, batchSvc *service.Batch, cfg *config.Config, logger *utils.Logger) error {
	count, err := batchSvc.ResetStaleProcessingTasks(ctx, cfg.Consumer.Reclaim.StaleTaskAfter)
	if err != nil {
		return fmt.Errorf("reset stale processing tasks: %w", err)
	}
	if count > 0 {
		logger.Info(nil, "reclaimed %d stale processing tasks (age > %ds)", count, cfg.Consumer.Reclaim.StaleTaskAfter)
	}
	return nil
}

func consumeNextQueuedBatch(ctx context.Context, client *database.Client, batchSvc *service.Batch, maxConcurrent int, logger *utils.Logger) error {
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

	logger.Info(nil, "consuming queued batch %s", batch.ID)

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
		logger.Error(nil, "pre-create owner for batch %s: %v", batch.ID, err)
	}

	// --force so the child overwrites our placeholder owner row.
	cmd := exec.Command(os.Args[0], "consume", "--batch", batch.ID, "--force")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		logger.Error(nil, "fork consume for batch %s: %v — requeueing", batch.ID, err)
		if requeueErr := batchSvc.RequeueBatch(ctx, batch.ID); requeueErr != nil {
			logger.Error(nil, "requeue batch %s after fork failure: %v", batch.ID, requeueErr)
		}
		return nil
	}
	go func() { cmd.Wait() }()

	logger.Info(nil, "forked kushim consume --batch %s (PID %d)", batch.ID, cmd.Process.Pid)
	return nil
}

func runPollingLoop(ctx context.Context, c *Container, getClient func() *database.Client, getBatchSvc func() *service.Batch, maxConcurrent int) {
	var missingTools []config.ExternalTool
	var lastCfg *config.Config

	for {
		cfgPtr := c.cfg.Load()
		if cfgPtr != lastCfg {
			missingTools = config.MissingExternalToolErrors(cfgPtr)
			lastCfg = cfgPtr
		}

		// Without this, the polling loop keeps polling the old database after a reconnection.
		client := getClient()
		batchSvc := getBatchSvc()

		pollingCfg := cfgPtr.Consumer.Polling
		interval := max(time.Duration(pollingCfg.Interval)*time.Minute, time.Minute)

		if pollingCfg.Enabled && config.IsWithinActiveWindows(pollingCfg.Windows) {
			locked, lockErr := client.Queries.IsBackupLocked(ctx)
			if lockErr != nil {
				c.logger.Error(nil, "polling: backup lock check: %v", lockErr)
			} else if locked > 0 {
				c.logger.Debug(nil, "polling: skipped — backup in progress")
			} else {
				pollingTick(ctx, c, client, batchSvc, maxConcurrent, missingTools)
			}
		} else if pollingCfg.Enabled {
			c.logger.Debug(nil, "polling: disabled — outside active windows")
		} else {
			c.logger.Debug(nil, "polling: disabled")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func pollingTick(ctx context.Context, c *Container, client *database.Client, batchSvc *service.Batch, maxConcurrent int, missingTools []config.ExternalTool) {
	queuedCount, err := batchSvc.CountQueuedBatches(ctx)
	if err != nil {
		c.logger.Error(nil, "polling: count queued batches: %v", err)
		return
	}
	liveCount, err := batchSvc.CountLiveBatches(ctx)
	if err != nil {
		c.logger.Error(nil, "polling: count live batches: %v", err)
		return
	}
	if queuedCount+liveCount >= int64(maxConcurrent) {
		c.logger.Debug(nil, "polling: skipped — %d live, %d queued (max %d)", liveCount, queuedCount, maxConcurrent)
		return
	}

	if len(missingTools) > 0 {
		names := make([]string, len(missingTools))
		for i, t := range missingTools {
			names[i] = t.Engine
		}
		c.logger.Error(nil, "polling: skipped — missing external tools: %s", strings.Join(names, ", "))
		return
	}

	batchID, enqueued, err := consumption.ScanAndEnqueue(ctx, c.cfg.Load(), client, c.logger)
	if err != nil {
		c.logger.Error(nil, "polling: scan and enqueue: %v", err)
		return
	}
	if batchID == "" {
		c.logger.Debug(nil, "polling: no new files")
		return
	}
	if enqueued == 0 {
		c.logger.Info(nil, "polling: all files were duplicates (batch %s)", batchID)
		return
	}

	c.logger.Info(nil, "polling: batch %s created with %d files", batchID, enqueued)
}

func startPool(c *Container, ctx context.Context, name string, started *bool, pp **pool.Pool) error {
	if *started {
		return nil
	}
	p, err := c.GetPool(name)
	if err != nil {
		return err
	}
	p.Start(ctx)
	*pp = p
	*started = true
	c.logger.Info(nil, "%s pool started (delayed)", name)
	return nil
}

func stopPool(c *Container, name string, started *bool, pp **pool.Pool) {
	if !*started || *pp == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	(*pp).Stop(stopCtx)
	*pp = nil
	*started = false
}

func maybeScheduleTask(ctx context.Context, c *Container, client *database.Client, taskType string, active func(context.Context) (int64, error), due func(context.Context) (bool, error), payload map[string]any) error {
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

	dispatcher, err := c.GetDispatcher()
	if err != nil {
		return fmt.Errorf("get dispatcher: %w", err)
	}

	payloadJSON, _ := json.Marshal(payload)
	taskID := uuid.New().String()
	if _, err := dispatcher.Enqueue(ctx, taskType, "", payloadJSON, taskID); err != nil {
		return fmt.Errorf("enqueue %s task: %w", taskType, err)
	}

	c.logger.Info(nil, "%s task %s scheduled", taskType, taskID)
	return nil
}

func maybeScheduleBackup(ctx context.Context, c *Container, client *database.Client, cfg *config.Config) error {
	if !cfg.Backup.Enabled {
		return nil
	}

	for _, schedule := range cfg.Backup.Schedules {
		s := schedule
		payload := map[string]any{
			"mode": s.Mode,
			"path": s.Path,
			"keep": s.Keep,
		}
		if err := maybeScheduleTask(ctx, c, client, "backup",
			func(ctx context.Context) (int64, error) { return client.Queries.CountActiveBackupTasks(ctx) },
			func(ctx context.Context) (bool, error) { return backup.IsBackupDue(ctx, client.Queries, s) },
			payload,
		); err != nil {
			return err
		}
	}
	return nil
}

func maybeScheduleMirror(ctx context.Context, c *Container, client *database.Client, cfg *config.Config) error {
	if !cfg.Mirror.Enabled {
		return nil
	}

	payload := map[string]any{
		"path":     cfg.Mirror.Path,
		"interval": cfg.Mirror.Interval,
	}
	return maybeScheduleTask(ctx, c, client, "mirror",
		func(ctx context.Context) (int64, error) { return client.Queries.CountActiveMirrorTasks(ctx) },
		func(ctx context.Context) (bool, error) { return backup.IsMirrorDue(ctx, client.Queries, cfg.Mirror) },
		payload,
	)
}

func maybeScheduleThumbnailBackfill(ctx context.Context, c *Container, client *database.Client, cfg *config.Config, lastRun *time.Time) error {
	if !cfg.Consumer.Thumbnail.Enabled || cfg.Consumer.Thumbnail.BackfillInterval <= 0 {
		return nil
	}

	if lastRun.IsZero() {
		lastBatch, err := client.Queries.GetLastThumbnailBackfillBatchCreatedAt(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get last thumbnail backfill batch: %w", err)
		}
		if err == nil {
			*lastRun = lastBatch.Time
		}
	}
	// Anchor a fresh schedule to the preferred time so the first run honors off-hours.
	if lastRun.IsZero() {
		*lastRun = previousTimeOfDay(time.Now(), cfg.Consumer.Thumbnail.BackfillTime)
	}

	next := backup.NextBackupTime(*lastRun, cfg.Consumer.Thumbnail.BackfillInterval, cfg.Consumer.Thumbnail.BackfillTime)
	if time.Now().Before(next) {
		return nil
	}

	active, err := client.Queries.CountActiveThumbnailBackfillBatches(ctx)
	if err != nil {
		return fmt.Errorf("check active thumbnail backfill batches: %w", err)
	}
	if active > 0 {
		return nil
	}

	cmd := exec.Command(os.Args[0], "thumbnails", "--all")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		c.logger.Error(nil, "fork kushim thumbnails --all: %v", err)
		return nil
	}
	go func() { cmd.Wait() }()

	// Advance only after a successful fork so a failed start retries next tick.
	*lastRun = time.Now()
	c.logger.Info(nil, "forked kushim thumbnails --all (PID %d)", cmd.Process.Pid)
	return nil
}

func previousTimeOfDay(now time.Time, hhmm string) time.Time {
	pref := "02:00"
	if hhmm != "" {
		pref = hhmm
	}
	prefTime, err := time.Parse("15:04", pref)
	if err != nil {
		prefTime, _ = time.Parse("15:04", "02:00")
	}
	loc := now.Location()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), prefTime.Hour(), prefTime.Minute(), 0, 0, loc)
	if candidate.After(now) {
		candidate = candidate.AddDate(0, 0, -1)
	}
	return candidate
}

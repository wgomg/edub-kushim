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

	pidFile := filepath.Join(c.config.App.ConfigDir, "kushim-queue.pid")
	logFile := filepath.Join(c.config.App.ConfigDir, "logs", "queue.log")

	os.MkdirAll(filepath.Dir(logFile), 0755)
	if err := c.logger.SetLogFile(utils.LogFileConfig{
		Path:       logFile,
		MaxSize:    c.config.App.Logging.MaxSize,
		MaxBackups: c.config.App.Logging.MaxBackups,
		MaxAge:     c.config.App.Logging.MaxAge,
		Compress:   c.config.App.Logging.Compress,
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

	batchSvc := service.NewBatch(client, c.config.Consumer.Reclaim.MaxRetries)
	maxConcurrent := c.config.Srv.MaxConcurrentBatches
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}

	c.logger.Info(nil, "queue daemon started (max %d concurrent batches)", maxConcurrent)

	if c.config.Backup.Enabled {
		backupPool, err := c.GetPool("backup")
		if err != nil {
			return fmt.Errorf("get backup pool: %w", err)
		}
		backupPool.Start(ctx)
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			backupPool.Stop(shutdownCtx)
		}()
	}

	go runPollingLoop(ctx, c, client, batchSvc, maxConcurrent)

	notifyCh := make(chan struct{}, 4)
	go listenForBatchNotifications(ctx, config.BuildPostgresDSN(c.config.Db), notifyCh, c.logger)

	safetyInterval := 30 * time.Second
	safetyTimer := time.NewTimer(safetyInterval)
	defer safetyTimer.Stop()

	hkTicker := time.NewTicker(5 * time.Second)
	defer hkTicker.Stop()

	var lastStaleTaskClaim time.Time

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
			if c.config.Backup.Enabled {
				locked, lockErr := client.Queries.IsBackupLocked(ctx)
				if lockErr != nil {
					c.logger.Error(nil, "backup lock check: %v", lockErr)
				} else if locked == 0 {
					if err := maybeScheduleBackup(ctx, c); err != nil {
						c.logger.Error(nil, "backup scheduling: %v", err)
					}
				}
			}

			if c.config.Consumer.Reclaim.Enabled {
				if err := reclaimStaleBatches(ctx, c.config, client, batchSvc, c.logger); err != nil {
					c.logger.Error(nil, "stale reclamation: %v", err)
				}
				minInterval := max(c.config.Consumer.Reclaim.StaleTaskAfter/10, 60)
				if time.Since(lastStaleTaskClaim) > time.Duration(minInterval)*time.Second {
					if err := reclaimStaleTasks(ctx, batchSvc, c.config, c.logger); err != nil {
						c.logger.Error(nil, "stale task reclamation: %v", err)
					}
					lastStaleTaskClaim = time.Now()
				}
			}

		case <-ctx.Done():
			c.logger.Info(nil, "queue daemon stopped")
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

func runPollingLoop(ctx context.Context, c *Container, client *database.Client, batchSvc *service.Batch, maxConcurrent int) {
	var missingTools []config.ExternalTool
	var lastReloaded bool

	for {
		// Re-read config from disk so that polling enabled/interval/windows changes
		// take effect without requiring a daemon restart.
		reloaded, rErr := config.Reload(c.config.App.ConfigDir, c.config)
		if rErr != nil {
			c.logger.Error(nil, "polling: reload config: %v", rErr)
		}

		// Only recompute missing tools when config was reloaded (tool landscape
		// is deterministic at runtime and doesn't change without a config update).
		if reloaded || !lastReloaded {
			missingTools = config.MissingExternalToolErrors(c.config)
		}
		lastReloaded = reloaded

		cfg := c.config.Consumer.Polling
		interval := max(time.Duration(cfg.Interval)*time.Minute, time.Minute)

		if cfg.Enabled && config.IsWithinActiveWindows(cfg.Windows) {
			locked, lockErr := client.Queries.IsBackupLocked(ctx)
			if lockErr != nil {
				c.logger.Error(nil, "polling: backup lock check: %v", lockErr)
			} else if locked > 0 {
				c.logger.Debug(nil, "polling: skipped — backup in progress")
			} else {
				pollingTick(ctx, c, client, batchSvc, maxConcurrent, missingTools)
			}
		} else if cfg.Enabled {
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

	batchID, enqueued, err := consumption.ScanAndEnqueue(ctx, c.config, client, c.logger)
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

func maybeScheduleBackup(ctx context.Context, c *Container) error {
	if !c.config.Backup.Enabled {
		return nil
	}

	state, err := backup.ReadState(c.config.App.ConfigDir)
	if err != nil {
		return fmt.Errorf("read backup state: %w", err)
	}

	if !backup.ShouldSchedule(state, c.config.Backup.Interval, c.config.Backup.Time) {
		return nil
	}

	dispatcher, err := c.GetDispatcher()
	if err != nil {
		return fmt.Errorf("get dispatcher: %w", err)
	}

	taskID := uuid.New().String()
	payload, _ := json.Marshal(map[string]any{})
	if _, err := dispatcher.Enqueue(ctx, "backup", "", payload, taskID); err != nil {
		return fmt.Errorf("enqueue backup task: %w", err)
	}

	nextRun := backup.NextRunTime(c.config.Backup.Interval, c.config.Backup.Time)
	state.NextScheduled = nextRun.Format(time.RFC3339)
	if err := backup.WriteState(c.config.App.ConfigDir, state); err != nil {
		return fmt.Errorf("write backup state: %w", err)
	}

	c.logger.Info(nil, "backup task %s scheduled (next run: %s)", taskID, state.NextScheduled)
	return nil
}

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
							stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							backupPool.Stop(stopCtx)
							cancel()
							backupPool = nil
							backupPoolStarted = false
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
					var poolErr error
					backupPool, poolErr = c.GetPool("backup")
					if poolErr != nil {
						c.logger.Error(nil, "backup pool: %v", poolErr)
					} else {
						backupPool.Start(ctx)
						backupPoolStarted = true
						c.logger.Info(nil, "backup pool started (delayed)")
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
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				backupPool.Stop(stopCtx)
				cancel()
				backupPool = nil
				backupPoolStarted = false
				c.logger.Info(nil, "backup pool stopped (backup disabled)")
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
			if backupPool != nil && backupPoolStarted {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				backupPool.Stop(shutdownCtx)
				cancel()
			}
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

func maybeScheduleBackup(ctx context.Context, c *Container, client *database.Client, cfg *config.Config) error {
	if !cfg.Backup.Enabled {
		return nil
	}

	active, err := client.Queries.CountActiveBackupTasks(ctx)
	if err != nil {
		return fmt.Errorf("check active backup tasks: %w", err)
	}
	if active > 0 {
		return nil
	}

	for _, schedule := range cfg.Backup.Schedules {
		due, err := backup.IsBackupDue(ctx, client.Queries, schedule)
		if err != nil {
			return fmt.Errorf("check backup due (%s): %w", schedule.Mode, err)
		}
		if !due {
			continue
		}

		dispatcher, err := c.GetDispatcher()
		if err != nil {
			return fmt.Errorf("get dispatcher: %w", err)
		}

		payload, _ := json.Marshal(map[string]any{
			"mode": schedule.Mode,
			"path": schedule.Path,
			"keep": schedule.Keep,
		})
		taskID := uuid.New().String()
		if _, err := dispatcher.Enqueue(ctx, "backup", "", payload, taskID); err != nil {
			return fmt.Errorf("enqueue backup task (%s): %w", schedule.Mode, err)
		}

		c.logger.Info(nil, "backup task %s scheduled (mode=%s)", taskID, schedule.Mode)
	}
	return nil
}

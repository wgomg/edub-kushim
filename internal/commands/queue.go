package commands

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
	"syscall"
	"time"

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
	if err := c.logger.SetLogFile(logFile); err != nil {
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

	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
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

	batchSvc := service.NewBatch(client.Queries)
	maxConcurrent := c.config.Srv.MaxConcurrentBatches
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}

	c.logger.Info(nil, "queue daemon started (max %d concurrent batches)", maxConcurrent)

	go runPollingLoop(ctx, c, client, batchSvc, maxConcurrent)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if c.config.Consumer.Reclaim.Enabled {
				if err := reclaimStaleBatches(ctx, batchSvc, c.logger); err != nil {
					c.logger.Error(nil, "stale reclamation: %v", err)
				}
			}
			if err := consumeNextQueuedBatch(ctx, client, batchSvc, maxConcurrent, c.logger); err != nil {
				c.logger.Error(nil, "queue consumption: %v", err)
			}
		case <-ctx.Done():
			c.logger.Info(nil, "queue daemon stopped")
			return nil
		}
	}
}

func reclaimStaleBatches(ctx context.Context, batchSvc *service.Batch, logger *utils.Logger) error {
	staleIDs, err := batchSvc.ListStaleBatchOwners(ctx)
	if err != nil {
		return fmt.Errorf("list stale batch owners: %w", err)
	}
	for _, batchID := range staleIDs {
		logger.Info(nil, "reclaiming stale batch %s", batchID)
		if _, err := batchSvc.ResetProcessingTasksByBatch(ctx, batchID); err != nil {
			logger.Error(nil, "reset processing tasks for batch %s: %v", batchID, err)
			continue
		}
		if err := batchSvc.RequeueBatch(ctx, batchID); err != nil {
			logger.Error(nil, "requeue batch %s: %v", batchID, err)
			continue
		}
		if err := batchSvc.DeleteBatchOwnerByBatchID(ctx, batchID); err != nil {
			logger.Error(nil, "delete batch owner for batch %s: %v", batchID, err)
			continue
		}
		logger.Info(nil, "stale batch %s reclaimed and requeued", batchID)
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
			pollingTick(ctx, c, client, batchSvc, maxConcurrent, missingTools)
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

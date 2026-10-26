// Package queue is the queue daemon: it LISTENs for batch and lock notifications, polls as a safety net,
// claims queued batches through the dispatcher — forking each batch's task CLI in batch mode — runs the
// declarative scheduler and requeues stale work through the reaper.
package queue

import (
	"context"
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
	sched    *scheduler

	notifyCancel context.CancelFunc
	notifyCh     chan struct{}
	dispatcher   *dispatch.Dispatcher

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

	catalog, err := task.NewCatalog(map[types.TaskType]task.TypeWiring{
		types.Task.Type.Consume:       {DedupKey: consumption.DedupKey},
		types.Task.Type.Backup:        {DedupKey: backup.BackupDedupKey},
		types.Task.Type.ThumbBackfill: {DedupKey: service.ThumbnailDedupKey},
		types.Task.Type.Mirror: {
			DedupKey:      backup.MirrorDedupKey,
			Preconditions: []task.Precondition{backup.MirrorPrecondition},
		},
		types.Task.Type.TagReconcile: {DedupKey: task.ConstantDedupKey(curation.TagReconcileDedupKey)},
		types.Task.Type.TagMeasure:   {DedupKey: task.ConstantDedupKey(tags.TagMeasureDedupKey)},
		types.Task.Type.TagIdentity:  {DedupKey: task.ConstantDedupKey(curation.TagIdentityDedupKey)},
		types.Task.Type.TagAudit:     {DedupKey: task.ConstantDedupKey(curation.TagAuditDedupKey)},
	})
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
		sched:    newScheduler(c.GetLogger()),
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

	d.sched.run(ctx, client.Queries, enqueuer, cfg)

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

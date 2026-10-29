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

	// reloadCh and stagedCfg carry the config watcher's handoff to the loop, which owns every swap that
	// applyConfig performs; pendingCfg holds a tree whose database reconnect failed, retried on each tick
	reloadCh   chan struct{}
	stagedCfg  atomic.Pointer[config.Config]
	pendingCfg *config.Config

	lastStaleTaskClaim time.Time
}

// queueAuxConns is the daemon's fixed connection headroom beyond the slot budget: the control loop, the two
// background producer goroutines (the inbox scan and the thumbnail backfill) and a margin of two for the
// lock-holding children the slot budget does not gate; their exit callbacks are the only children the budget
// term does not count, because it admits one per in-flight no-holds child.
const queueAuxConns = 5

// queueConnBudget is the daemon pool's open-connection ceiling: the concurrency its slot budget admits
// plus the loop and background-producer users.
func queueConnBudget(cfg *config.Config) int {
	return dispatch.EffectiveBudget(cfg.Srv.MaxConcurrentBatches) + queueAuxConns
}

// Run is the kushim queue verb: it runs the daemon loop until signalled, dispatching queued batches,
// applying reloaded configuration, scheduling the recurring runs, forking each batch's task-CLI child
// and reaping stale work.
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

	c.GetLogger().Info("", "queue daemon started")

	d := &daemon{
		c:        c,
		catalog:  catalog,
		sched:    newScheduler(ctx, c.GetLogger()),
		batchSvc: service.NewBatch(client, c.GetConfig().Consumer.Reclaim.MaxRetries),
		reloadCh: make(chan struct{}, 1),
	}
	d.client.Store(client)
	d.applyConnBudget(c.GetConfig())

	d.startNotify(ctx)
	defer d.stopNotify()

	wake := func() {
		select {
		case d.notifyCh <- struct{}{}:
		default:
		}
	}
	budget := func() int { return c.GetConfig().Srv.MaxConcurrentBatches }
	d.dispatcher = dispatch.NewDispatcher(catalog, c.GetLogger(), types.HostQueue, budget, wake, nil)

	watcher := config.NewWatcher(c.GetConfig().App.ConfigDir, 5*time.Second, d.stageConfig, c.GetLogger())
	watcher.Start()
	defer watcher.Stop()

	safetyInterval := 30 * time.Second
	safetyTimer := time.NewTimer(safetyInterval)
	defer safetyTimer.Stop()

	hkTicker := time.NewTicker(5 * time.Second)
	defer hkTicker.Stop()

	for {
		select {
		case <-d.reloadCh:
			if newCfg := d.stagedCfg.Swap(nil); newCfg != nil {
				d.applyConfig(ctx, newCfg)
			}

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
	if d.pendingCfg != nil {
		d.applyConfig(ctx, d.pendingCfg)
	}
	cfg := d.c.GetConfig()
	client := d.client.Load()
	enqueuer := task.NewEnqueuer(client, d.catalog)

	d.sched.run(ctx, client, enqueuer, cfg)

	d.reclaimStale(ctx, cfg)
}

// stageConfig is the config watcher's handoff: it parks the freshest tree and pokes the loop, which owns
// the swap itself. A change that arrives while an earlier one is still staged replaces it, so the loop
// always applies the newest configuration.
func (d *daemon) stageConfig(cfg *config.Config) {
	d.stagedCfg.Store(cfg)
	select {
	case d.reloadCh <- struct{}{}:
	default:
	}
}

// applyConfig installs a configuration tree the watcher loaded and applies the effects that outlive the
// snapshot swap: a database connection change reconnects the client — draining the scheduler's background
// producers once the new pool is ready and before the old one closes — rebuilds the batch service and
// restarts the notification listener. It runs on the daemon loop, so a reload can never interleave with a
// dispatch sweep using the client it replaces, and the drain cannot race a producer being spawned. A failed
// reconnect keeps the previous tree and leaves the new one pending, so the next housekeeping tick retries it.
func (d *daemon) applyConfig(ctx context.Context, newCfg *config.Config) {
	oldCfg := d.c.GetConfig()
	d.c.SetConfig(newCfg)
	d.applyConnBudget(newCfg)
	if !config.DatabaseConnectionChanged(oldCfg.Db, newCfg.Db) {
		d.pendingCfg = nil
		return
	}

	d.c.GetLogger().Info("", "DB config changed, reconnecting...")
	// the reconnect opens the new pool before it touches the old one, so a connection that never comes up
	// leaves the producers working on the healthy old pool alone instead of canceling them on every retry
	// only once the new pool is ready is the generation drained, right before the old pool closes
	newClient, err := d.c.ReconnectClient(d.sched.stopBackgroundJobs)
	if err != nil {
		d.c.GetLogger().Error("", "failed to reconnect to new DB: %v — keeping previous configuration", err)
		d.c.SetConfig(oldCfg)
		d.applyConnBudget(oldCfg)
		d.pendingCfg = newCfg
		return
	}

	d.client.Store(newClient)
	d.applyConnBudget(newCfg)
	d.batchSvc = service.NewBatch(newClient, newCfg.Consumer.Reclaim.MaxRetries)

	d.stopNotify()
	d.startNotify(ctx)
	d.pendingCfg = nil
}

// applyConnBudget re-derives the daemon pool's ceiling from the configuration tree being applied. It
// runs at startup and on every applied tree, because max_concurrent_batches is hot-reloadable and the
// dispatcher's budget supplier already sees edits on the next sweep.
func (d *daemon) applyConnBudget(cfg *config.Config) {
	if client := d.client.Load(); client != nil {
		client.DB().SetMaxOpenConns(queueConnBudget(cfg))
	}
}

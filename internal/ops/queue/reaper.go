package queue

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
)

// staleOwnerPageLimit bounds one recovery pass so the housekeeping tick cannot stall the daemon loop behind
// an arbitrarily long stale-owner sweep; the remaining owners are recovered oldest first on the next tick,
// about thirteen per second. A persistently failing owner keeps occupying a page slot and delays the owners
// behind it across passes, which is the price of the bound: its own failures are logged per owner.
const staleOwnerPageLimit = 64

func (d *daemon) reclaimStale(ctx context.Context, cfg *config.Config) {
	if !cfg.Consumer.Reclaim.Enabled {
		return
	}
	if err := reclaimStaleBatches(ctx, cfg, d.client.Load(), d.batchSvc, d.catalog, d.c.GetLogger()); err != nil {
		d.c.GetLogger().Error("", "stale reclamation: %v", err)
	}
	minInterval := max(cfg.Consumer.Reclaim.StaleTaskAfter/10, 60)
	if time.Since(d.lastStaleTaskClaim) > time.Duration(minInterval)*time.Second {
		if err := reclaimStaleTasks(ctx, d.client.Load(), d.batchSvc, cfg, d.c.GetLogger()); err != nil {
			d.c.GetLogger().Error("", "stale task reclamation: %v", err)
		}
		d.lastStaleTaskClaim = time.Now()
	}
}

func reclaimStaleBatches(ctx context.Context, cfg *config.Config, client *database.Client, batchSvc *service.Batch, catalog task.TypeCatalog, logger *log.Logger) error {
	owners, err := batchSvc.ListStaleBatchOwners(ctx, staleOwnerPageLimit)
	if err != nil {
		return fmt.Errorf("list stale batch owners: %w", err)
	}
	for _, owner := range owners {
		logger.Info("", "reclaiming stale batch %s (owner=%s pid=%d)", owner.BatchID, owner.OwnerID, owner.Pid)
		// release only the exact row this pass listed — same pid, still stale — so a fresh owner that took
		// the batch over after the snapshot, or one that adopted or heartbeated since, keeps its lease and
		// its run, and the whole iteration is skipped.
		released, err := client.Queries.ReleaseStaleBatchOwner(ctx, database.ReleaseStaleBatchOwnerParams{
			BatchID:           owner.BatchID,
			OwnerID:           owner.OwnerID,
			Pid:               owner.Pid,
			StaleAfterSeconds: task.LeaseStaleSeconds,
		})
		if err != nil {
			logger.Error("", "release stale batch owner for batch %s: %v", owner.BatchID, err)
			continue
		}
		if released == 0 {
			logger.Info("", "stale batch %s changed owner — leaving it to the new execution", owner.BatchID)
			continue
		}
		// signal only after the release landed: the terminated pid is the one whose stale lease this pass
		// actually held, never a process that revalidated its lease after the snapshot was taken. The release
		// also satisfies the settle guard and is the identity fence for every later recovery step
		if task.IsAlive(owner.Pid) {
			if err := syscall.Kill(int(owner.Pid), syscall.SIGTERM); err != nil {
				logger.Error("", "signal SIGTERM to PID %d for batch %s: %v", owner.Pid, owner.BatchID, err)
			}
		}

		reset, err := batchSvc.ResetProcessingTasksByBatch(ctx, owner.BatchID)
		if err != nil {
			logger.Error("", "reset processing tasks for batch %s: %v", owner.BatchID, err)
			requeueReleasedBatch(ctx, client, logger, owner.BatchID)
			continue
		}
		if err := consumption.QuarantineFailedFiles(ctx, client.Queries, cfg.Storage.StorageDir, logger,
			owner.BatchID, int32(cfg.Consumer.Reclaim.MaxRetries), reset.ConsumeTaskIDs()); err != nil {
			logger.Error("", "quarantine files for batch %s: %v", owner.BatchID, err)
		}

		hasWork, err := batchSvc.HasPendingWork(ctx, owner.BatchID)
		if err != nil {
			logger.Error("", "check pending work for batch %s: %v", owner.BatchID, err)
			requeueReleasedBatch(ctx, client, logger, owner.BatchID)
			continue
		}
		if !hasWork {
			status, settleErr := task.SettleBatch(ctx, client, logger, catalog, task.Execution{BatchID: owner.BatchID})
			if settleErr != nil {
				logger.Error("", "settle stale batch %s: %v", owner.BatchID, settleErr)
				requeueReleasedBatch(ctx, client, logger, owner.BatchID)
				continue
			}
			if status.Terminal() {
				logger.Info("", "stale batch %s settled as %s — no work left", owner.BatchID, status)
				continue
			}
			// work appeared between the check and the settle; with the lease already gone nothing else would
			// pick the batch up, so hand it back to the dispatcher
			logger.Info("", "stale batch %s could not settle (%s) — requeueing", owner.BatchID, status)
			requeueReleasedBatch(ctx, client, logger, owner.BatchID)
		} else {
			if _, err := client.Queries.RequeueProcessingBatch(ctx, owner.BatchID); err != nil {
				logger.Error("", "requeue batch %s: %v", owner.BatchID, err)
				continue
			}
			logger.Info("", "stale batch %s reclaimed and requeued", owner.BatchID)
		}
	}
	if len(owners) == staleOwnerPageLimit {
		logger.Info("", "stale-owner page full (%d handled this pass)", staleOwnerPageLimit)
	}
	return nil
}

// requeueReleasedBatch hands a batch whose stale lease was already released back to the dispatcher, so a
// recovery step that fails after the release cannot leave an ownerless processing batch behind.
func requeueReleasedBatch(ctx context.Context, client *database.Client, logger *log.Logger, batchID string) {
	if _, err := client.Queries.RequeueProcessingBatch(ctx, batchID); err != nil {
		logger.Error("", "batch %s could not be requeued after its lease was released: %v (needs manual recovery)", batchID, err)
		return
	}
	logger.Info("", "batch %s requeued after its released recovery did not complete", batchID)
}

func reclaimStaleTasks(ctx context.Context, client *database.Client, batchSvc *service.Batch, cfg *config.Config, logger *log.Logger) error {
	reset, err := batchSvc.ResetStaleProcessingTasks(ctx, cfg.Consumer.Reclaim.StaleTaskAfter)
	if err != nil {
		return fmt.Errorf("reset stale processing tasks: %w", err)
	}
	consumeIDs := reset.ConsumeTaskIDs()
	if len(consumeIDs) > 0 {
		// the sweep's quarantined consume tasks are permanent failures, so their inbox files move right now
		// instead of waiting for the batch's next run
		if qErr := consumption.QuarantineFailedFiles(ctx, client.Queries, cfg.Storage.StorageDir, logger,
			"", int32(cfg.Consumer.Reclaim.MaxRetries), consumeIDs); qErr != nil {
			logger.Error("", "quarantine files for stale-swept tasks: %v", qErr)
		}
	}
	if reset.Reset > 0 || len(reset.Quarantined) > 0 {
		logger.Info("", "reclaimed %d stale processing tasks (%d quarantined %v, age > %ds)",
			reset.Reset, len(reset.Quarantined), consumeIDs, cfg.Consumer.Reclaim.StaleTaskAfter)
	}
	return nil
}

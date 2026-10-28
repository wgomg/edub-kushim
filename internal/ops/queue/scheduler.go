package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// The schedule names scheduled batches carry in batch.schedule. Mirror, thumbnail backfill and the tag
// passes keep the interim names batches already carry, so their due-ness continues from the last terminal
// run; backup names are per mode, so each configured mode keeps its own cadence.
const (
	scheduleMirror            = "mirror"
	scheduleThumbnailBackfill = "thumbnail-backfill"
	scheduleTagReconcile      = "tagreconcile"
	scheduleTagMeasure        = "tagmeasure"
	scheduleTagIdentity       = "tagidentity"
	scheduleTagAudit          = "tagaudit"

	backupSchedulePrefix = "backup:"
)

// schedulerEntry is one recurring enqueue: the batch schedule name it writes (empty for the unnamed
// inbox-polling entry), the origin its batches carry, when it is enabled and due, and the producer that
// builds the run's task set. It is data, not execution: it carries no callback that runs work, no dispatcher
// reference and no query of its own. A producer may return any task types — chained waiting children
// included, exactly like the ingest producers — and the enqueue validates the set; the entry owns no task
// type. timing is read only for named entries: the unnamed entry's due path reads the polling interval from
// the configuration, so the days-typed ScheduleTiming never carries a minutes value.
type schedulerEntry struct {
	name    string
	origin  types.BatchOrigin
	enabled func(*config.Config) bool
	timing  func(*config.Config) backup.ScheduleTiming
	produce func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error)
}

// label names the entry in logs: its batch schedule name, or "polling" for the unnamed entry.
func (e schedulerEntry) label() string {
	if e.name == "" {
		return "polling"
	}
	return e.name
}

// entriesFor returns the entry table of the current configuration: one backup entry per configured mode
// plus the fixed mirror, thumbnail-backfill and tag-pass entries, then the unnamed inbox-polling entry.
// It runs on every evaluation, so enabled predicates, timings and producers read the freshly reloaded
// configuration instead of values captured at daemon start.
func entriesFor(cfg *config.Config) []schedulerEntry {
	entries := make([]schedulerEntry, 0, len(cfg.Backup.Schedules)+7)
	if cfg.Backup.Enabled {
		for _, entry := range cfg.Backup.Schedules {
			s := entry
			entries = append(entries, schedulerEntry{
				name:    backupSchedulePrefix + s.Mode,
				origin:  types.Batch.Origin.Schedule,
				enabled: func(*config.Config) bool { return true },
				timing: func(*config.Config) backup.ScheduleTiming {
					return backup.ScheduleTiming{Interval: s.Interval, Time: s.Time}
				},
				produce: func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error) {
					return backup.BackupSpecs(s.Mode, s.Path, s.Keep,
						backup.ScheduleTiming{Interval: s.Interval, Time: s.Time})
				},
			})
		}
	}

	entries = append(entries,
		schedulerEntry{
			name:    scheduleMirror,
			origin:  types.Batch.Origin.Schedule,
			enabled: func(cfg *config.Config) bool { return cfg.Mirror.Enabled },
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: cfg.Mirror.Interval, Time: cfg.Mirror.Time}
			},
			produce: func(_ context.Context, cfg *config.Config, _ *database.Client, _ *log.Logger) ([]task.TaskSpec, error) {
				return backup.MirrorSpecs(cfg.Mirror.Path, cfg.Mirror.Interval,
					backup.ScheduleTiming{Interval: cfg.Mirror.Interval, Time: cfg.Mirror.Time})
			},
		},
		schedulerEntry{
			name:   scheduleThumbnailBackfill,
			origin: types.Batch.Origin.Schedule,
			enabled: func(cfg *config.Config) bool {
				return cfg.Consumer.Thumbnail.Enabled && cfg.Consumer.Thumbnail.BackfillInterval > 0
			},
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{
					Interval: cfg.Consumer.Thumbnail.BackfillInterval,
					Time:     cfg.Consumer.Thumbnail.BackfillTime,
				}
			},
			produce: func(ctx context.Context, _ *config.Config, client *database.Client, _ *log.Logger) ([]task.TaskSpec, error) {
				return service.ThumbnailBackfillSpecs(ctx, client.Queries)
			},
		},
		schedulerEntry{
			name:    scheduleTagReconcile,
			origin:  types.Batch.Origin.Schedule,
			enabled: func(cfg *config.Config) bool { return cfg.Enricher.TagReconcile.Enabled },
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: cfg.Enricher.TagReconcile.Interval, Time: cfg.Enricher.TagReconcile.Time}
			},
			produce: func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error) {
				return curation.ReconcileSpecs(), nil
			},
		},
		schedulerEntry{
			name:    scheduleTagMeasure,
			origin:  types.Batch.Origin.Schedule,
			enabled: func(cfg *config.Config) bool { return cfg.Enricher.TagMeasure.Enabled },
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: cfg.Enricher.TagMeasure.Interval, Time: cfg.Enricher.TagMeasure.Time}
			},
			produce: func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error) {
				return curation.MeasureSpecs(), nil
			},
		},
		schedulerEntry{
			name:    scheduleTagIdentity,
			origin:  types.Batch.Origin.Schedule,
			enabled: func(cfg *config.Config) bool { return cfg.Enricher.TagAdjudicator.Enabled },
			timing: func(*config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: 1, Time: "03:30"}
			},
			produce: func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error) {
				return curation.IdentitySpecs(), nil
			},
		},
		schedulerEntry{
			name:    scheduleTagAudit,
			origin:  types.Batch.Origin.Schedule,
			enabled: func(cfg *config.Config) bool { return cfg.Enricher.TagAudit.Enabled },
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: cfg.Enricher.TagAudit.Interval, Time: cfg.Enricher.TagAudit.Time}
			},
			produce: func(context.Context, *config.Config, *database.Client, *log.Logger) ([]task.TaskSpec, error) {
				return curation.AuditSpecs(), nil
			},
		},
	)

	return append(entries, pollingEntry())
}

// pollingEntry is the unnamed recurring entry: the inbox scan. Its batches carry origin polling and no
// schedule name, it declares no timing, and its producer skips the occurrence while maintenance holds the
// lock or an external tool is missing, exactly as the hand-rolled polling loop did. Overlap is governed by
// the consume dedup keys rather than a name-keyed active-batch check.
func pollingEntry() schedulerEntry {
	return schedulerEntry{
		origin: types.Batch.Origin.Polling,
		enabled: func(cfg *config.Config) bool {
			return cfg.Consumer.Polling.Enabled && config.IsWithinActiveWindows(cfg.Consumer.Polling.Windows)
		},
		produce: func(ctx context.Context, cfg *config.Config, client *database.Client, logger *log.Logger) ([]task.TaskSpec, error) {
			locked, err := client.Queries.IsLockHeld(ctx, string(types.LockMaintenance))
			if err != nil {
				return nil, fmt.Errorf("maintenance lock check: %w", err)
			}
			if locked > 0 {
				logger.Debug("", "polling: skipped — backup in progress")
				return nil, nil
			}
			if missing := config.MissingExternalToolErrors(cfg); len(missing) > 0 {
				names := make([]string, len(missing))
				for i, tool := range missing {
					names[i] = tool.Engine
				}
				logger.Error("", "polling: skipped — missing external tools: %s", strings.Join(names, ", "))
				return nil, nil
			}
			return consumption.ScanInboxSpecs(ctx, cfg, client, logger)
		},
	}
}

// scheduler is the queue daemon's single recurring-enqueue loop over the declarative entries. It holds the
// process-boundary logger it reports through, firstDue — the slot a never-run named entry is waiting for, so
// its first occurrence waits for the next preferred time of day instead of firing on the first evaluation —
// and lastFire, the last dispatch of the unnamed interval entry, whose cadence the batch table cannot carry
// because a scan that finds nothing creates no batch. scanInFlight keeps two inbox scans from overlapping:
// the scan runs on its own goroutine because slow storage I/O must never stall the daemon's dispatch,
// reclamation or shutdown loop. firstDue and lastFire are touched only by the evaluation loop, never by a
// scan goroutine. Once a named entry has a terminal batch, due-ness comes from that batch and firstDue is
// irrelevant.
type scheduler struct {
	logger       *log.Logger
	firstDue     map[string]time.Time
	lastFire     map[string]time.Time
	scanInFlight atomic.Bool
}

func newScheduler(logger *log.Logger) *scheduler {
	return &scheduler{
		logger:   logger,
		firstDue: make(map[string]time.Time),
		lastFire: make(map[string]time.Time),
	}
}

// run evaluates every entry of the current configuration once. A failure in one entry is logged and does
// not stop the remaining ones.
func (s *scheduler) run(ctx context.Context, client *database.Client, enqueuer *task.Enqueuer, cfg *config.Config) {
	for _, entry := range entriesFor(cfg) {
		if err := s.evaluate(ctx, client, enqueuer, cfg, entry); err != nil {
			s.logger.Error("", "%s: %v", entry.label(), err)
		}
	}
}

// evaluate enqueues one entry's occurrence when it is enabled and due. A named entry is idle while a
// batch of its name is active; the unnamed entry carries no name-keyed state and relies on task dedup
// keys for overlap. The unnamed entry dispatches asynchronously and refuses a new occurrence while its
// previous scan is in flight; its dispatch is recorded in lastFire before the in-flight check.
func (s *scheduler) evaluate(ctx context.Context, client *database.Client, enqueuer *task.Enqueuer, cfg *config.Config, entry schedulerEntry) error {
	if !entry.enabled(cfg) {
		return nil
	}

	if entry.name != "" {
		active, err := client.Queries.CountActiveBatchesForSchedule(ctx, entry.name)
		if err != nil {
			return fmt.Errorf("count active batches: %w", err)
		}
		if active > 0 {
			s.logger.Debug("", "%s: skipped — %d active batch(es)", entry.label(), active)
			return nil
		}
	}

	due, err := s.entryDue(ctx, client.Queries, cfg, entry)
	if err != nil {
		return fmt.Errorf("check due: %w", err)
	}
	if !due {
		return nil
	}

	if entry.name == "" {
		// anchor the dispatch before the in-flight check: an occurrence that is refused or fails waits a full
		// interval instead of retrying on every tick
		s.lastFire[entry.name] = time.Now()
		if !s.scanInFlight.CompareAndSwap(false, true) {
			s.logger.Debug("", "%s: previous scan still running; occurrence skipped", entry.label())
			return nil
		}
		go func() {
			defer s.scanInFlight.Store(false)
			if err := s.fire(ctx, client, enqueuer, cfg, entry); err != nil {
				s.logger.Error("", "%s: %v", entry.label(), err)
			}
		}()
		return nil
	}

	return s.fire(ctx, client, enqueuer, cfg, entry)
}

// fire builds one entry's task set and enqueues it with the entry's origin and schedule name. An enqueue
// whose task type has an unmet environment precondition skips the occurrence at info level; a zero-enqueued
// result creates nothing and leaves a named entry due. The unnamed entry logs that its scan fired without
// creating work instead of claiming a schedule stays due.
func (s *scheduler) fire(ctx context.Context, client *database.Client, enqueuer *task.Enqueuer, cfg *config.Config, entry schedulerEntry) error {
	tasks, err := entry.produce(ctx, cfg, client, s.logger)
	if err != nil {
		return fmt.Errorf("build tasks: %w", err)
	}

	result, err := enqueueEntryBatch(ctx, enqueuer, entry, tasks)
	if err != nil {
		if precondition, ok := errors.AsType[*task.PreconditionError](err); ok {
			s.logger.Info("", "%s: occurrence skipped — %v", entry.label(), precondition)
			return nil
		}
		return err
	}
	if result.BatchID == "" {
		if entry.name == "" {
			s.logger.Debug("", "%s: nothing to enqueue this occurrence", entry.label())
		} else {
			s.logger.Debug("", "%s: nothing to enqueue; schedule stays due", entry.label())
		}
		return nil
	}

	s.logger.Info("", "%s enqueued (batch %s, %d tasks)", entry.label(), result.BatchID, len(result.TaskIDs))
	return nil
}

// entryDue reports whether the entry's next occurrence has arrived. A named entry anchors on the last
// terminal batch of its name; the unnamed entry anchors on its last dispatch.
func (s *scheduler) entryDue(ctx context.Context, queries *database.Queries, cfg *config.Config, entry schedulerEntry) (bool, error) {
	if entry.name == "" {
		return s.intervalDue(entry.name, cfg, time.Now()), nil
	}
	return s.due(ctx, queries, entry.name, entry.timing(cfg))
}

// intervalDue reports whether the unnamed entry's interval has elapsed since its last dispatch. An empty
// map means the daemon just started, so the first scan runs immediately.
func (s *scheduler) intervalDue(name string, cfg *config.Config, now time.Time) bool {
	interval := max(time.Duration(cfg.Consumer.Polling.Interval)*time.Minute, time.Minute)
	last, ok := s.lastFire[name]
	return !ok || now.Sub(last) >= interval
}

// due reports whether a named entry's next occurrence has arrived. The last terminal batch of the entry's
// schedule name anchors the interval, so a failed run waits for the next occurrence rather than retrying
// every tick. A named entry with no terminal batch yet waits for the next preferred slot, anchored once
// per daemon lifetime.
func (s *scheduler) due(ctx context.Context, queries *database.Queries, name string, timing backup.ScheduleTiming) (bool, error) {
	last, err := queries.GetLastTerminalBatchCreatedAtForSchedule(ctx, name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	now := time.Now()
	if err == nil && last.Valid {
		return !now.Before(backup.NextBackupTime(last.Time, timing.Interval, timing.Time)), nil
	}

	first, ok := s.firstDue[name]
	if !ok {
		first = backup.NextOccurrence(now, timing.Time)
		s.firstDue[name] = first
	}
	return !now.Before(first), nil
}

// enqueueEntryBatch enqueues one entry's producer-built task set, returning the full result. The entry's
// origin and schedule name are set here, at the scheduler boundary; NewBatchSpec rejects the mismatch.
func enqueueEntryBatch(ctx context.Context, enqueuer *task.Enqueuer, entry schedulerEntry, tasks []task.TaskSpec) (task.EnqueueResult, error) {
	spec, err := task.NewBatchSpec(entry.origin, entry.name)
	if err != nil {
		return task.EnqueueResult{}, fmt.Errorf("build %s batch spec: %w", entry.label(), err)
	}
	return enqueuer.EnqueueBatch(ctx, spec, tasks)
}

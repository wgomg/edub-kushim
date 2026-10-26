package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
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

// scheduleDefinition is one recurring enqueue: its name and task type, when it is enabled and due, and the
// producer that builds the run's task set. It is data, not execution: it carries no callback that runs
// work, no dispatcher reference and no query of its own. Due-ness and active state are computed centrally
// from batch.schedule, and the scheduler only enqueues.
type scheduleDefinition struct {
	name     string
	taskType types.TaskType
	enabled  func(*config.Config) bool
	timing   func(*config.Config) backup.ScheduleTiming
	produce  func(context.Context, *config.Config, *database.Queries) ([]task.TaskSpec, error)
}

// schedulesFor returns the schedule set of the current configuration: one backup definition per configured
// mode plus the fixed mirror, thumbnail-backfill and tag-pass schedules. It runs on every evaluation, so
// enabled predicates, timings and producers read the freshly reloaded configuration instead of values
// captured at daemon start.
func schedulesFor(cfg *config.Config) []scheduleDefinition {
	schedules := make([]scheduleDefinition, 0, len(cfg.Backup.Schedules)+6)
	if cfg.Backup.Enabled {
		for _, entry := range cfg.Backup.Schedules {
			s := entry
			schedules = append(schedules, scheduleDefinition{
				name:     backupSchedulePrefix + s.Mode,
				taskType: types.Task.Type.Backup,
				enabled:  func(*config.Config) bool { return true },
				timing: func(*config.Config) backup.ScheduleTiming {
					return backup.ScheduleTiming{Interval: s.Interval, Time: s.Time}
				},
				produce: func(context.Context, *config.Config, *database.Queries) ([]task.TaskSpec, error) {
					return backup.BackupSpecs(s.Mode, s.Path, s.Keep,
						backup.ScheduleTiming{Interval: s.Interval, Time: s.Time})
				},
			})
		}
	}

	return append(schedules,
		scheduleDefinition{
			name:     scheduleMirror,
			taskType: types.Task.Type.Mirror,
			enabled:  func(cfg *config.Config) bool { return cfg.Mirror.Enabled },
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: cfg.Mirror.Interval, Time: cfg.Mirror.Time}
			},
			produce: func(_ context.Context, cfg *config.Config, _ *database.Queries) ([]task.TaskSpec, error) {
				return backup.MirrorSpecs(cfg.Mirror.Path, cfg.Mirror.Interval,
					backup.ScheduleTiming{Interval: cfg.Mirror.Interval, Time: cfg.Mirror.Time})
			},
		},
		scheduleDefinition{
			name:     scheduleThumbnailBackfill,
			taskType: types.Task.Type.ThumbBackfill,
			enabled: func(cfg *config.Config) bool {
				return cfg.Consumer.Thumbnail.Enabled && cfg.Consumer.Thumbnail.BackfillInterval > 0
			},
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{
					Interval: cfg.Consumer.Thumbnail.BackfillInterval,
					Time:     cfg.Consumer.Thumbnail.BackfillTime,
				}
			},
			produce: func(ctx context.Context, _ *config.Config, queries *database.Queries) ([]task.TaskSpec, error) {
				return service.ThumbnailBackfillSpecs(ctx, queries)
			},
		},
		scheduleDefinition{
			name:     scheduleTagReconcile,
			taskType: types.Task.Type.TagReconcile,
			enabled:  func(cfg *config.Config) bool { return cfg.Enricher.TagReconcile.Enabled },
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: cfg.Enricher.TagReconcile.Interval, Time: cfg.Enricher.TagReconcile.Time}
			},
			produce: func(context.Context, *config.Config, *database.Queries) ([]task.TaskSpec, error) {
				return curation.ReconcileSpecs(), nil
			},
		},
		scheduleDefinition{
			name:     scheduleTagMeasure,
			taskType: types.Task.Type.TagMeasure,
			enabled:  func(cfg *config.Config) bool { return cfg.Enricher.TagMeasure.Enabled },
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: cfg.Enricher.TagMeasure.Interval, Time: cfg.Enricher.TagMeasure.Time}
			},
			produce: func(context.Context, *config.Config, *database.Queries) ([]task.TaskSpec, error) {
				return curation.MeasureSpecs(), nil
			},
		},
		scheduleDefinition{
			name:     scheduleTagIdentity,
			taskType: types.Task.Type.TagIdentity,
			enabled:  func(cfg *config.Config) bool { return cfg.Enricher.TagAdjudicator.Enabled },
			timing: func(*config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: 1, Time: "03:30"}
			},
			produce: func(context.Context, *config.Config, *database.Queries) ([]task.TaskSpec, error) {
				return curation.IdentitySpecs(), nil
			},
		},
		scheduleDefinition{
			name:     scheduleTagAudit,
			taskType: types.Task.Type.TagAudit,
			enabled:  func(cfg *config.Config) bool { return cfg.Enricher.TagAudit.Enabled },
			timing: func(cfg *config.Config) backup.ScheduleTiming {
				return backup.ScheduleTiming{Interval: cfg.Enricher.TagAudit.Interval, Time: cfg.Enricher.TagAudit.Time}
			},
			produce: func(context.Context, *config.Config, *database.Queries) ([]task.TaskSpec, error) {
				return curation.AuditSpecs(), nil
			},
		},
	)
}

// scheduler is the queue daemon's single scheduling loop over the declarative definitions. It holds the
// process-boundary logger it reports through and firstDue, the one thing the batch table cannot answer —
// the slot a never-run schedule is waiting for — so its first occurrence waits for the next preferred
// time of day instead of firing on the first evaluation. Once a batch of the schedule exists, due-ness
// comes from the last terminal batch and the entry is irrelevant.
type scheduler struct {
	logger   *log.Logger
	firstDue map[string]time.Time
}

func newScheduler(logger *log.Logger) *scheduler {
	return &scheduler{logger: logger, firstDue: make(map[string]time.Time)}
}

// run evaluates every schedule of the current configuration once. A failure in one schedule is logged and
// does not stop the remaining ones.
func (s *scheduler) run(ctx context.Context, queries *database.Queries, enqueuer *task.Enqueuer, cfg *config.Config) {
	for _, def := range schedulesFor(cfg) {
		if err := s.evaluate(ctx, queries, enqueuer, cfg, def); err != nil {
			s.logger.Error("", "%s schedule: %v", def.name, err)
		}
	}
}

// evaluate enqueues one schedule's occurrence when it is enabled, idle and due: the producer builds the
// task set, the enqueue creates the batch atomically and a zero-enqueued result leaves the schedule due.
func (s *scheduler) evaluate(ctx context.Context, queries *database.Queries, enqueuer *task.Enqueuer, cfg *config.Config, def scheduleDefinition) error {
	if !def.enabled(cfg) {
		return nil
	}

	active, err := queries.CountActiveBatchesForSchedule(ctx, def.name)
	if err != nil {
		return fmt.Errorf("count active batches: %w", err)
	}
	if active > 0 {
		s.logger.Debug("", "%s: skipped — %d active batch(es)", def.name, active)
		return nil
	}

	due, err := s.due(ctx, queries, def.name, def.timing(cfg))
	if err != nil {
		return fmt.Errorf("check due: %w", err)
	}
	if !due {
		return nil
	}

	tasks, err := def.produce(ctx, cfg, queries)
	if err != nil {
		return fmt.Errorf("build tasks: %w", err)
	}
	for _, spec := range tasks {
		if spec.Type != def.taskType {
			return fmt.Errorf("producer built a %q task for a %q schedule", spec.Type, def.taskType)
		}
	}

	result, err := enqueueScheduledBatch(ctx, enqueuer, def.name, tasks)
	if err != nil {
		if precondition, ok := errors.AsType[*task.PreconditionError](err); ok {
			s.logger.Info("", "%s: occurrence skipped — %v", def.name, precondition)
			return nil
		}
		return err
	}
	if result.BatchID == "" {
		s.logger.Debug("", "%s: nothing to enqueue; schedule stays due", def.name)
		return nil
	}

	s.logger.Info("", "%s scheduled (batch %s, %d tasks)", def.name, result.BatchID, len(result.TaskIDs))
	return nil
}

// due reports whether the schedule's next occurrence has arrived. The last terminal batch of the schedule
// name anchors the interval, so a failed run waits for the next occurrence rather than retrying every tick.
// A schedule with no terminal batch yet waits for the next preferred slot, anchored once per daemon
// lifetime.
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

// enqueueScheduledBatch enqueues the producer-built task set of one scheduled run, returning the full
// result. The schedule name and origin are set here, at the scheduler boundary.
func enqueueScheduledBatch(ctx context.Context, enqueuer *task.Enqueuer, scheduleName string, tasks []task.TaskSpec) (task.EnqueueResult, error) {
	spec, err := task.NewBatchSpec(types.Batch.Origin.Schedule, scheduleName)
	if err != nil {
		return task.EnqueueResult{}, fmt.Errorf("build %s batch spec: %w", scheduleName, err)
	}
	return enqueuer.EnqueueBatch(ctx, spec, tasks)
}

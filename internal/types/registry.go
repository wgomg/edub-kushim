// Package types is the single Go declaration point for the task-system vocabulary: the batch sources,
// origins and statuses, the task types and statuses, the tag verdicts, and the closed set of resource
// locks (LockMaintenance, LockStorage), all exposed as namespaced constant bags: types.Batch.Source,
// types.Batch.Origin and types.Task.Type, with parse helpers for the wire form. The database's enum
// columns mirror these values. It also holds the shared CRUD result envelope (CreateResult, UpdateResult,
// DeleteResult and their status types) and the TaskCreator and BatchCreator interfaces the enqueue paths
// accept.
package types

import (
	"fmt"
	"slices"
)

// BatchSource says which flow created a batch.
type BatchSource string

// BatchOrigin names the entry point that requested a batch; it is requester metadata only, never the
// dispatch policy.
type BatchOrigin string

// LockName names one of the closed set of resource locks an execution holds or waits for.
type LockName string

// The declared resource locks. Only these names exist: LockMaintenance pauses pipeline work while a
// maintenance run executes; LockStorage serialises bulk storage writes with per-document writes.
const (
	LockMaintenance LockName = "maintenance"
	LockStorage     LockName = "storage"
)

// BatchStatus is the lifecycle state of a batch.
type BatchStatus string

// TaskType names one kind of unit of work in the queue.
type TaskType string

// TaskStatus is the lifecycle state of a task.
type TaskStatus string

// TagVerdict is the judgement the pair evidence recorded.
type TagVerdict string

// Batch groups the batch source, origin and status wire values.
var Batch = struct {
	Source struct {
		CLI             BatchSource
		API             BatchSource
		Upload          BatchSource
		Polling         BatchSource
		Reenrich        BatchSource
		OrphanedRestore BatchSource
		Thumbbackfill   BatchSource
		Backup          BatchSource
		Mirror          BatchSource
		Config          BatchSource
		TagReconcile    BatchSource
		TagMeasure      BatchSource
		TagIdentity     BatchSource
		TagAudit        BatchSource
	}
	Origin struct {
		CLI      BatchOrigin
		API      BatchOrigin
		Polling  BatchOrigin
		Schedule BatchOrigin
		Setup    BatchOrigin
		Wizard   BatchOrigin
	}
	Status struct {
		Queued     BatchStatus
		Processing BatchStatus
		Completed  BatchStatus
		Failed     BatchStatus
		Paused     BatchStatus
		Cancelled  BatchStatus
	}
}{
	Source: struct {
		CLI             BatchSource
		API             BatchSource
		Upload          BatchSource
		Polling         BatchSource
		Reenrich        BatchSource
		OrphanedRestore BatchSource
		Thumbbackfill   BatchSource
		Backup          BatchSource
		Mirror          BatchSource
		Config          BatchSource
		TagReconcile    BatchSource
		TagMeasure      BatchSource
		TagIdentity     BatchSource
		TagAudit        BatchSource
	}{
		CLI:             "cli",
		API:             "api",
		Upload:          "upload",
		Polling:         "polling",
		Reenrich:        "reenrich",
		OrphanedRestore: "orphaned-restore",
		Thumbbackfill:   "thumbbackfill",
		Backup:          "backup",
		Mirror:          "mirror",
		Config:          "config",
		TagReconcile:    "tagreconcile",
		TagMeasure:      "tagmeasure",
		TagIdentity:     "tagidentity",
		TagAudit:        "tagaudit",
	},
	Origin: struct {
		CLI      BatchOrigin
		API      BatchOrigin
		Polling  BatchOrigin
		Schedule BatchOrigin
		Setup    BatchOrigin
		Wizard   BatchOrigin
	}{
		CLI:      "cli",
		API:      "api",
		Polling:  "polling",
		Schedule: "schedule",
		Setup:    "setup",
		Wizard:   "wizard",
	},
	Status: struct {
		Queued     BatchStatus
		Processing BatchStatus
		Completed  BatchStatus
		Failed     BatchStatus
		Paused     BatchStatus
		Cancelled  BatchStatus
	}{
		Queued:     "queued",
		Processing: "processing",
		Completed:  "completed",
		Failed:     "failed",
		Paused:     "paused",
		Cancelled:  "cancelled",
	},
}

// Task groups the task-type and task-status values the queue and the
// handlers share.
var Task = struct {
	Type struct {
		Consume       TaskType
		Enrich        TaskType
		Thumbnail     TaskType
		Backup        TaskType
		Mirror        TaskType
		Config        TaskType
		TagReconcile  TaskType
		ThumbBackfill TaskType
		TagMeasure    TaskType
		TagIdentity   TaskType
		TagAudit      TaskType
	}
	Status struct {
		Pending    TaskStatus
		Processing TaskStatus
		Completed  TaskStatus
		Failed     TaskStatus
		Cancelled  TaskStatus
		Discarded  TaskStatus
		Waiting    TaskStatus
	}
}{
	Type: struct {
		Consume       TaskType
		Enrich        TaskType
		Thumbnail     TaskType
		Backup        TaskType
		Mirror        TaskType
		Config        TaskType
		TagReconcile  TaskType
		ThumbBackfill TaskType
		TagMeasure    TaskType
		TagIdentity   TaskType
		TagAudit      TaskType
	}{
		Consume:       "consume",
		Enrich:        "enrich",
		Thumbnail:     "thumbnail",
		Backup:        "backup",
		Mirror:        "mirror",
		Config:        "config",
		TagReconcile:  "tagreconcile",
		ThumbBackfill: "thumbbackfill",
		TagMeasure:    "tagmeasure",
		TagIdentity:   "tagidentity",
		TagAudit:      "tagaudit",
	},
	Status: struct {
		Pending    TaskStatus
		Processing TaskStatus
		Completed  TaskStatus
		Failed     TaskStatus
		Cancelled  TaskStatus
		Discarded  TaskStatus
		Waiting    TaskStatus
	}{
		Pending:    "pending",
		Processing: "processing",
		Completed:  "completed",
		Failed:     "failed",
		Cancelled:  "cancelled",
		Discarded:  "discarded",
		Waiting:    "waiting",
	},
}

// ParseBatchSource accepts one of the declared source values.
func ParseBatchSource(s string) (BatchSource, error) {
	v := BatchSource(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid batch source %q", s)
	}
	return v, nil
}

// TagVerdicts groups the verdict wire values with their canonical
// spelling.
var TagVerdicts = struct {
	Same    TagVerdict
	Variant TagVerdict
	Related TagVerdict
}{
	Same:    "same",
	Variant: "variant",
	Related: "related",
}

// ParseTagVerdict accepts one of the declared verdict values.
func ParseTagVerdict(s string) (TagVerdict, error) {
	v := TagVerdict(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid tag verdict %q", s)
	}
	return v, nil
}

// Valid reports whether the verdict is one of the declared values.
func (v TagVerdict) Valid() bool {
	return slices.Contains(allTagVerdicts, v)
}

// AllTagVerdicts returns every declared verdict, cloned.
func AllTagVerdicts() []TagVerdict { return slices.Clone(allTagVerdicts) }

// ParseBatchStatus accepts one of the declared status values.
func ParseBatchStatus(s string) (BatchStatus, error) {
	v := BatchStatus(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid batch status %q", s)
	}
	return v, nil
}

// ParseTaskType accepts one of the declared task types.
func ParseTaskType(s string) (TaskType, error) {
	v := TaskType(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid task type %q", s)
	}
	return v, nil
}

// ParseTaskStatus accepts one of the declared task statuses.
func ParseTaskStatus(s string) (TaskStatus, error) {
	v := TaskStatus(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid task status %q", s)
	}
	return v, nil
}

// Valid reports whether the source is declared.
func (s BatchSource) Valid() bool {
	return slices.Contains(allBatchSources, s)
}

// Valid reports whether the origin is declared.
func (o BatchOrigin) Valid() bool {
	return slices.Contains(allBatchOrigins, o)
}

// Valid reports whether the status is declared.
func (s BatchStatus) Valid() bool {
	return slices.Contains(allBatchStatuses, s)
}

// Valid reports whether the task type is declared.
func (t TaskType) Valid() bool {
	return slices.Contains(allTaskTypes, t)
}

// Valid reports whether the task status is declared.
func (t TaskStatus) Valid() bool {
	return slices.Contains(allTaskStatuses, t)
}

// IsBlocking reports whether the type is a blocking-task protocol type:
// it forks its own process and owns its batch.
func (t TaskType) IsBlocking() bool {
	return slices.Contains(blockingTaskTypes, t)
}

// IsLockGated reports whether the type runs under the maintenance lock.
func (t TaskType) IsLockGated() bool {
	return slices.Contains(lockGatedTaskTypes, t)
}

// AllBatchSources returns every declared source, cloned.
func AllBatchSources() []BatchSource { return slices.Clone(allBatchSources) }

// AllBatchStatuses returns every declared batch status, cloned.
func AllBatchStatuses() []BatchStatus { return slices.Clone(allBatchStatuses) }

// AllTaskTypes returns every declared task type, cloned.
func AllTaskTypes() []TaskType { return slices.Clone(allTaskTypes) }

// AllTaskStatuses returns every declared task status, cloned.
func AllTaskStatuses() []TaskStatus { return slices.Clone(allTaskStatuses) }

// LockGatedTaskTypes lists the task types the maintenance lock gates; the
// lock holder runs them alone.
func LockGatedTaskTypes() []TaskType { return slices.Clone(lockGatedTaskTypes) }

// BlockingTaskTypes lists the blocking-task protocol types, the ones
// with dedicated child processes and batch ownership.
func BlockingTaskTypes() []TaskType { return slices.Clone(blockingTaskTypes) }

// NonConsumeBatchSources are the sources whose batches own their lifecycle
// in their handlers and never occupy a consume slot.
func NonConsumeBatchSources() []BatchSource { return slices.Clone(nonConsumeBatchSources) }

// NonConsumeBatchSourceValues is NonConsumeBatchSources in plain string form, for the queued-batch
// exclusion queries: the same registry list backs both, so the exclusion cannot drift from the sources.
func NonConsumeBatchSourceValues() []string {
	values := make([]string, len(nonConsumeBatchSources))
	for i, src := range nonConsumeBatchSources {
		values[i] = string(src)
	}
	return values
}

var allBatchSources = []BatchSource{
	Batch.Source.CLI,
	Batch.Source.API,
	Batch.Source.Upload,
	Batch.Source.Polling,
	Batch.Source.Reenrich,
	Batch.Source.OrphanedRestore,
	Batch.Source.Thumbbackfill,
	Batch.Source.Backup,
	Batch.Source.Mirror,
	Batch.Source.Config,
	Batch.Source.TagReconcile,
	Batch.Source.TagMeasure,
	Batch.Source.TagIdentity,
	Batch.Source.TagAudit,
}

var nonConsumeBatchSources = []BatchSource{
	Batch.Source.Config,
	Batch.Source.Backup,
	Batch.Source.Mirror,
	Batch.Source.TagReconcile,
	Batch.Source.TagMeasure,
	Batch.Source.TagIdentity,
	Batch.Source.TagAudit,
}

var allBatchOrigins = []BatchOrigin{
	Batch.Origin.CLI,
	Batch.Origin.API,
	Batch.Origin.Polling,
	Batch.Origin.Schedule,
	Batch.Origin.Setup,
	Batch.Origin.Wizard,
}

var allBatchStatuses = []BatchStatus{
	Batch.Status.Queued,
	Batch.Status.Processing,
	Batch.Status.Completed,
	Batch.Status.Failed,
	Batch.Status.Paused,
	Batch.Status.Cancelled,
}

var allTaskTypes = []TaskType{
	Task.Type.Consume,
	Task.Type.Enrich,
	Task.Type.Thumbnail,
	Task.Type.Backup,
	Task.Type.Mirror,
	Task.Type.Config,
	Task.Type.TagReconcile,
	Task.Type.ThumbBackfill,
	Task.Type.TagMeasure,
	Task.Type.TagIdentity,
	Task.Type.TagAudit,
}

var blockingTaskTypes = []TaskType{
	Task.Type.Backup,
	Task.Type.Mirror,
	Task.Type.TagReconcile,
	Task.Type.ThumbBackfill,
	Task.Type.TagMeasure,
	Task.Type.TagIdentity,
	Task.Type.TagAudit,
}

var lockGatedTaskTypes = []TaskType{
	Task.Type.Consume,
	Task.Type.Enrich,
	Task.Type.Thumbnail,
}

var allTaskStatuses = []TaskStatus{
	Task.Status.Pending,
	Task.Status.Processing,
	Task.Status.Completed,
	Task.Status.Failed,
	Task.Status.Cancelled,
	Task.Status.Discarded,
	Task.Status.Waiting,
}

var allTagVerdicts = []TagVerdict{
	TagVerdicts.Same,
	TagVerdicts.Variant,
	TagVerdicts.Related,
}

// Package types is the single Go declaration point for the task-system vocabulary: the batch origins
// and statuses, the task types and statuses, the tag verdicts, and the closed set of resource locks
// (LockMaintenance, LockStorage), all exposed as namespaced constant bags: types.Batch.Origin and the
// types.Task.Type bag, with parse helpers for the wire form. The database's enum columns mirror these
// values. It also holds the shared CRUD result envelope: CreateResult, UpdateResult, DeleteResult and
// their status types.
package types

import (
	"fmt"
	"slices"
)

// BatchOrigin names the entry point that requested a batch; it is requester metadata only, never the
// dispatch policy.
type BatchOrigin string

// LockName names one of the closed set of resource locks an execution holds or waits for.
type LockName string

// Host names a process allowed to execute a batch: the kushim queue or its child, or the setup wizard.
type Host string

// The declared batch execution hosts. A task type definition lists the hosts eligible to dispatch its
// batches.
const (
	HostQueue  Host = "queue"
	HostWizard Host = "wizard"
)

// The declared resource locks. Only these names exist: LockMaintenance pauses pipeline work while a
// maintenance run executes; LockStorage serialises bulk storage writes with per-document writes.
const (
	LockMaintenance LockName = "maintenance"
	LockStorage     LockName = "storage"
)

// LockDeclaration is the queued lock policy declared for one task type: the named locks an execution
// of that type holds for its whole batch run, and the locks a claim must see free before it may start.
// Both slices are unordered sets; an empty slice declares no lock in that direction.
type LockDeclaration struct {
	Holds    []LockName
	WaitsFor []LockName
}

// lockDeclarations is the single closed statement of which locks each task type holds and waits for.
// The task type definitions populate their queued lock policy from it, and the claim, drain and sweep
// paths read it directly, so the vocabulary below the composition root has one source.
var lockDeclarations = map[TaskType]LockDeclaration{
	Task.Type.Consume:        {WaitsFor: []LockName{LockMaintenance}},
	Task.Type.Enrich:         {WaitsFor: []LockName{LockMaintenance}},
	Task.Type.Thumbnail:      {WaitsFor: []LockName{LockMaintenance, LockStorage}},
	Task.Type.ThumbBackfill:  {Holds: []LockName{LockStorage}, WaitsFor: []LockName{LockMaintenance}},
	Task.Type.Backup:         {Holds: []LockName{LockMaintenance}, WaitsFor: []LockName{LockStorage}},
	Task.Type.Mirror:         {Holds: []LockName{LockMaintenance}, WaitsFor: []LockName{LockStorage}},
	Task.Type.TagReconcile:   {Holds: []LockName{LockMaintenance}},
	Task.Type.TagMeasure:     {Holds: []LockName{LockMaintenance}},
	Task.Type.TagIdentity:    {Holds: []LockName{LockMaintenance}},
	Task.Type.TagAudit:       {Holds: []LockName{LockMaintenance}},
	Task.Type.MigrateDB:      {Holds: []LockName{LockMaintenance}},
	Task.Type.MigrateStorage: {Holds: []LockName{LockMaintenance}, WaitsFor: []LockName{LockStorage}},
}

// BatchStatus is the lifecycle state of a batch.
type BatchStatus string

// Terminal reports whether the status ends a batch's lifecycle, so no transition may leave or overwrite it.
func (s BatchStatus) Terminal() bool {
	return s == Batch.Status.Completed || s == Batch.Status.Failed || s == Batch.Status.Cancelled
}

// TaskType names one kind of unit of work in the queue.
type TaskType string

// TaskStatus is the lifecycle state of a task.
type TaskStatus string

// TagVerdict is the judgement the pair evidence recorded.
type TagVerdict string

// Batch groups the batch origin and status wire values.
var Batch = struct {
	Origin struct {
		CLI      BatchOrigin
		API      BatchOrigin
		Polling  BatchOrigin
		Schedule BatchOrigin
		Setup    BatchOrigin
		Wizard   BatchOrigin
		Trigger  BatchOrigin
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
	Origin: struct {
		CLI      BatchOrigin
		API      BatchOrigin
		Polling  BatchOrigin
		Schedule BatchOrigin
		Setup    BatchOrigin
		Wizard   BatchOrigin
		Trigger  BatchOrigin
	}{
		CLI:      "cli",
		API:      "api",
		Polling:  "polling",
		Schedule: "schedule",
		Setup:    "setup",
		Wizard:   "wizard",
		Trigger:  "trigger",
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
		Consume        TaskType
		Enrich         TaskType
		Thumbnail      TaskType
		Backup         TaskType
		Mirror         TaskType
		MigrateDB      TaskType
		MigrateStorage TaskType
		TagReconcile   TaskType
		ThumbBackfill  TaskType
		TagMeasure     TaskType
		TagIdentity    TaskType
		TagAudit       TaskType
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
		Consume        TaskType
		Enrich         TaskType
		Thumbnail      TaskType
		Backup         TaskType
		Mirror         TaskType
		MigrateDB      TaskType
		MigrateStorage TaskType
		TagReconcile   TaskType
		ThumbBackfill  TaskType
		TagMeasure     TaskType
		TagIdentity    TaskType
		TagAudit       TaskType
	}{
		Consume:        "consume",
		Enrich:         "enrich",
		Thumbnail:      "thumbnail",
		Backup:         "backup",
		Mirror:         "mirror",
		MigrateDB:      "migrate-db",
		MigrateStorage: "migrate-storage",
		TagReconcile:   "tagreconcile",
		ThumbBackfill:  "thumbbackfill",
		TagMeasure:     "tagmeasure",
		TagIdentity:    "tagidentity",
		TagAudit:       "tagaudit",
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

// AllBatchStatuses returns every declared batch status, cloned.
func AllBatchStatuses() []BatchStatus { return slices.Clone(allBatchStatuses) }

// AllTaskTypes returns every declared task type, cloned.
func AllTaskTypes() []TaskType { return slices.Clone(allTaskTypes) }

// LockDeclarationFor returns the queued lock declaration of a task type; a type without a declaration
// declares neither locks nor waits. The returned slices are cloned, so callers cannot mutate the table.
func LockDeclarationFor(t TaskType) LockDeclaration {
	declaration := lockDeclarations[t]
	return LockDeclaration{
		Holds:    slices.Clone(declaration.Holds),
		WaitsFor: slices.Clone(declaration.WaitsFor),
	}
}

// HoldsLocks reports whether an execution of the type holds locks for its whole batch run; the batch
// claim acquires them and the settle, pause, cancel or reclamation releases them.
func (t TaskType) HoldsLocks() bool {
	return len(lockDeclarations[t].Holds) > 0
}

// TaskTypesWaitingOn lists the task types whose claims wait for at least one of the given locks. An
// execution holding those locks drains this set before starting its work.
func TaskTypesWaitingOn(locks ...LockName) []TaskType {
	waiting := make([]TaskType, 0, len(allTaskTypes))
	for _, taskType := range allTaskTypes {
		for _, wait := range lockDeclarations[taskType].WaitsFor {
			if slices.Contains(locks, wait) {
				waiting = append(waiting, taskType)
				break
			}
		}
	}
	return waiting
}

// LockHoldingTaskTypes lists the task types that hold locks for their whole batch run.
func LockHoldingTaskTypes() []TaskType {
	holding := make([]TaskType, 0, len(allTaskTypes))
	for _, taskType := range allTaskTypes {
		if taskType.HoldsLocks() {
			holding = append(holding, taskType)
		}
	}
	return holding
}

// AllLockNames returns every declared lock name, cloned.
func AllLockNames() []LockName { return slices.Clone(allLockNames) }

// LockNameValues is AllLockNames in plain string form, for the lock queries.
func LockNameValues() []string {
	names := AllLockNames()
	values := make([]string, len(names))
	for i, name := range names {
		values[i] = string(name)
	}
	return values
}

var allBatchOrigins = []BatchOrigin{
	Batch.Origin.CLI,
	Batch.Origin.API,
	Batch.Origin.Polling,
	Batch.Origin.Schedule,
	Batch.Origin.Setup,
	Batch.Origin.Wizard,
	Batch.Origin.Trigger,
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
	Task.Type.MigrateDB,
	Task.Type.MigrateStorage,
	Task.Type.TagReconcile,
	Task.Type.ThumbBackfill,
	Task.Type.TagMeasure,
	Task.Type.TagIdentity,
	Task.Type.TagAudit,
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

var allLockNames = []LockName{LockMaintenance, LockStorage}

var allTagVerdicts = []TagVerdict{
	TagVerdicts.Same,
	TagVerdicts.Variant,
	TagVerdicts.Related,
}

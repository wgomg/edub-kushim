// Package types is the single Go declaration point for the task-system vocabulary.
package types

import (
	"fmt"
	"slices"
)

type BatchSource string
type BatchStatus string
type TaskType string
type TaskStatus string

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

var Task = struct {
	Type struct {
		Consume   TaskType
		Enrich    TaskType
		Thumbnail TaskType
		Backup    TaskType
		Mirror    TaskType
		Config    TaskType
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
		Consume   TaskType
		Enrich    TaskType
		Thumbnail TaskType
		Backup    TaskType
		Mirror    TaskType
		Config    TaskType
	}{
		Consume:   "consume",
		Enrich:    "enrich",
		Thumbnail: "thumbnail",
		Backup:    "backup",
		Mirror:    "mirror",
		Config:    "config",
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

func ParseBatchSource(s string) (BatchSource, error) {
	v := BatchSource(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid batch source %q", s)
	}
	return v, nil
}

func ParseBatchStatus(s string) (BatchStatus, error) {
	v := BatchStatus(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid batch status %q", s)
	}
	return v, nil
}

func ParseTaskType(s string) (TaskType, error) {
	v := TaskType(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid task type %q", s)
	}
	return v, nil
}

func ParseTaskStatus(s string) (TaskStatus, error) {
	v := TaskStatus(s)
	if !v.Valid() {
		return "", fmt.Errorf("invalid task status %q", s)
	}
	return v, nil
}

func (s BatchSource) Valid() bool {
	return slices.Contains(allBatchSources, s)
}

func (s BatchStatus) Valid() bool {
	return slices.Contains(allBatchStatuses, s)
}

func (t TaskType) Valid() bool {
	return slices.Contains(allTaskTypes, t)
}

func (t TaskStatus) Valid() bool {
	return slices.Contains(allTaskStatuses, t)
}

func (t TaskType) IsBlocking() bool {
	return slices.Contains(blockingTaskTypes, t)
}

func (t TaskType) IsLockGated() bool {
	return slices.Contains(lockGatedTaskTypes, t)
}

func AllBatchSources() []BatchSource  { return slices.Clone(allBatchSources) }
func AllBatchStatuses() []BatchStatus { return slices.Clone(allBatchStatuses) }
func AllTaskTypes() []TaskType        { return slices.Clone(allTaskTypes) }
func AllTaskStatuses() []TaskStatus   { return slices.Clone(allTaskStatuses) }

func LockGatedTaskTypes() []TaskType { return slices.Clone(lockGatedTaskTypes) }

// NonConsumeBatchSources are the sources whose batches own their lifecycle
// in their handlers and never occupy a consume slot.
func NonConsumeBatchSources() []BatchSource { return slices.Clone(nonConsumeBatchSources) }

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
}

var nonConsumeBatchSources = []BatchSource{
	Batch.Source.Config,
	Batch.Source.Backup,
	Batch.Source.Mirror,
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
}

var blockingTaskTypes = []TaskType{
	Task.Type.Backup,
	Task.Type.Mirror,
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
